#!/usr/bin/env python3
"""sysupdate.py CONTAINER SRC SEED WANT [EXCLUDE...]

Update the /9front of a running 9front-fs container from commit SEED
(the tree's, /9front/dist/9front/.git/refs/heads/front) to commit WANT
of the git checkout SRC, as sysupdate.rc and git/branch do inside
9front, but with the host's git, in seconds:

- only the files that differ between SEED and WANT are written, with
  git's modes as 664 or 775 and the time now; new files and
  directories belong to sys, changed files keep their owners;
- files edited in the tree get a three-way merge (git merge-file);
  a conflict, or a file edited in the tree but removed in WANT, fails;
  files changed in WANT but absent from the tree stay absent;
- removed files are removed;
- the tree's repository moves to WANT: a pack of SEED..WANT's objects,
  refs/heads/front, and INDEX9 lines, as git/branch writes them;
- /sys/lib/rootstub makes the directories every objtype installs into.

Top-level paths named in EXCLUDE (a host checkout's own files) are
left out of the tree, but not of the repository, as with sysupdate.rc.
"""
import hashlib, io, os, subprocess, sys, tarfile, tempfile, time

SYS = 110000
GIT = '9front/dist/9front/.git'

# Run in the container: a tar of the listed paths under /9front that
# exist, without descending into directories.
QUERY = r'''
import os, sys, tarfile
t = tarfile.open(fileobj=sys.stdout.buffer, mode='w|', format=tarfile.PAX_FORMAT)
for p in sys.stdin.buffer.read().split(b'\0'):
    p = os.fsdecode(p)
    if p and os.path.lexists('/9front/' + p):
        t.add('/9front/' + p, arcname='9front/' + p, recursive=False)
t.close()
'''


def die(msg):
    sys.exit('sysupdate: ' + msg)


def run(*cmd, input=None, check=True):
    r = subprocess.run(cmd, input=input, stdout=subprocess.PIPE)
    if check and r.returncode:
        die(f'{cmd[0]} {cmd[1]} failed')
    return r


def main(ctr, src, seed, want, *exclude):
    git = lambda *a, **k: run('git', '-C', src, *a, **k).stdout
    exec_ = lambda *a, **k: run('docker', 'exec', '-i', '-u', '0', '-w', '/9front', ctr, *a, **k).stdout
    fmt = git('rev-parse', '--show-object-format').decode().strip()
    blobid = lambda d: hashlib.new(fmt, b'blob %d\0' % len(d) + d).hexdigest()

    # :OLDMODE NEWMODE OLDID NEWID STATUS\0PATH\0, for files only
    raw = git('diff', '--raw', '-z', '--no-renames', '--no-abbrev', seed, want).split(b'\0')
    changes = []
    for meta, p in zip(raw[0::2], raw[1::2]):
        om, nm, oid, nid, st = meta.decode()[1:].split()
        changes.append((os.fsdecode(p), om, nm, oid, nid, st))
    if not changes:
        print('sysupdate: nothing to update', file=sys.stderr)
        return
    left = lambda p: p.split('/')[0] in exclude
    tree = [c for c in changes if not left(c[0])]

    # what the tree has of the changed files, their directories, and
    # the repository's front and INDEX9
    dirs = {os.path.dirname(c[0]) for c in tree}
    dirs = {d for p in dirs for d in [p] + [p.rsplit('/', i)[0] for i in range(1, p.count('/') + 1)] if d}
    ask = [c[0] for c in tree] + sorted(dirs) + ['dist/9front/.git/refs/heads/front', 'dist/9front/.git/INDEX9']
    have = {}
    with tarfile.open(fileobj=io.BytesIO(exec_('python3', '-c', QUERY, input=b'\0'.join(map(os.fsencode, ask)))), mode='r|') as t:
        for i in t:
            have[i.name[len('9front/'):]] = (i, t.extractfile(i).read() if i.isfile() else None)

    def blob(oid):
        return git('cat-file', 'blob', oid)

    out = io.BytesIO()
    tar = tarfile.open(fileobj=out, mode='w', format=tarfile.PAX_FORMAT)
    now = time.time()

    def add(p, data=None, mode=0o664, kind=tarfile.REGTYPE, link=''):
        i = tarfile.TarInfo('9front/' + p)
        i.mode, i.mtime, i.type, i.linkname, i.uid, i.gid = mode, now, kind, link, SYS, SYS
        i.size = len(data) if data is not None else 0
        tar.addfile(i, io.BytesIO(data) if data is not None else None)

    for d in sorted(d for d in dirs if d not in have or not have[d][0].isdir()):
        add(d, mode=0o775, kind=tarfile.DIRTYPE)
    remove, chown, merged, absent, conflicts = [], {}, [], [], []
    for p, om, nm, oid, nid, st in tree:
        h = have.get(p)
        cur = h[1] if h else None
        edited = h and om.startswith('100') and (cur is None or blobid(cur) != oid)
        if st == 'D':
            if edited:
                conflicts.append(p + ': edited in the tree, removed in ' + want[:9])
            elif h:
                remove.append(p)
            continue
        if st == 'M' and not h:
            absent.append(p)
            continue
        if h and (st == 'T' or not h[0].isfile()):
            remove.append(p)
            h = None
        if nm.startswith('160'):                # a submodule: nothing to write
            continue
        data = blob(nid)
        if edited and st == 'M' and blobid(cur) != nid:
            with tempfile.TemporaryDirectory() as td:
                for n, d in ('tree', cur), ('seed', blob(oid)), ('new', data):
                    open(f'{td}/{n}', 'wb').write(d)
                r = run('git', 'merge-file', '-p', '-L', 'tree', '-L', 'seed', '-L', want[:9],
                        f'{td}/tree', f'{td}/seed', f'{td}/new', check=False)
            if r.returncode:
                conflicts.append(p + ': merge conflict')
                continue
            data = r.stdout
            merged.append(p)
        if nm == '120000':
            add(p, kind=tarfile.SYMTYPE, link=os.fsdecode(data))
        else:
            add(p, data, 0o775 if nm == '100755' else 0o664)
        if h and (h[0].uid, h[0].gid) != (SYS, SYS):
            chown.setdefault(f'{h[0].uid}:{h[0].gid}', []).append(p)
    if conflicts:
        die('cannot update:\n\t' + '\n\t'.join(conflicts))

    # the repository: the objects, front, and git/branch's INDEX9 lines
    with tempfile.TemporaryDirectory() as td:
        objs = git('rev-list', '--objects', f'{seed}..{want}')
        name = run('git', '-C', src, 'pack-objects', '-q', f'{td}/p', input=objs).stdout.decode().strip()
        for ext in 'pack', 'idx':
            add(f'dist/9front/.git/objects/pack/{name}.{ext}', open(f'{td}/p-{name}.{ext}', 'rb').read())
    index = have['dist/9front/.git/INDEX9'][1] + b''.join(
        b'%s NOQID 0 %s\n' % (b'R' if c[5] == 'D' else b'T', os.fsencode(c[0])) for c in changes)
    for p, d in ('refs/heads/front', want.encode() + b'\n'), ('INDEX9', index):
        add('dist/9front/.git/' + p, d)
        i = have['dist/9front/.git/' + p][0]
        chown.setdefault(f'{i.uid}:{i.gid}', []).append('dist/9front/.git/' + p)
    tar.close()

    if remove:
        exec_('xargs', '-0', 'rm', '-rf', '--', input=b'\0'.join(map(os.fsencode, remove)))
    run('docker', 'cp', '-a', '-', f'{ctr}:/', input=out.getvalue())
    for owner, ps in chown.items():
        exec_('xargs', '-0', 'chown', owner, '--', input=b'\0'.join(map(os.fsencode, ps)))
    # the directories every objtype's builds install into, as sys
    run('docker', 'exec', '-w', '/9front', ctr, 'sh', '-c', 'umask 002; . sys/lib/rootstub')

    n = lambda s: sum(1 for c in tree if c[5] == s)
    print(f'sysupdate: {seed[:9]}..{want[:9]}: {n("A")} added, {n("M") + n("T")} changed, '
          f'{n("D")} removed, {len(merged)} merged', file=sys.stderr)
    for p in merged:
        print('sysupdate: merged:', p, file=sys.stderr)
    for p in absent:
        print('sysupdate: not in the tree, left out:', p, file=sys.stderr)
    for top in sorted({c[0].split('/')[0] for c in changes if left(c[0])}):
        print('sysupdate: left out:', top, file=sys.stderr)


if __name__ == '__main__':
    if len(sys.argv) < 5:
        sys.exit(__doc__.split('\n\n')[0])
    main(*sys.argv[1:])
