package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const unitTemplate = `[Unit]
Description=%s
Requires=%s
After=%s

[Service]
Type=simple
User=%s
Group=%s
%sWorkingDirectory=%s
ExecStart=%s start %s
ExecStartPost=%s wait %s
ExecStop=%s stop %s
TimeoutStartSec=120
TimeoutStopSec=120
Restart=no
UMask=0077

[Install]
WantedBy=multi-user.target
`

const netTemplate = `[Unit]
Description=%s
Before=%s

[Service]
Type=oneshot
ExecStart=%s net
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`

type unit struct{ name, text string }

// units returns the systemd units for the lab: its network, then a
// unit for each system, which needs the systems its kernel boots from.
func (x *ctx) units() ([]unit, error) {
	lab := x.lab
	if strings.Contains(x.e.self, "/go-build") {
		return nil, fmt.Errorf("units: %s is a go run binary: go build p9cloud and run that", x.e.self)
	}
	run := shquote(x.e.self) + " -f " + shquote(lab.Path)
	user, group, err := lab.Owner()
	if err != nil {
		return nil, err
	}
	net := lab.Prefix + "-network.service"
	var all []string
	for _, s := range lab.Systems {
		all = append(all, s.Unit())
	}
	us := []unit{{net, fmt.Sprintf(netTemplate,
		lab.Prefix+" bridge "+lab.Host.Or("bridge", "")+" and tap devices", strings.Join(all, " "), run)}}
	for _, s := range lab.Systems {
		req := []string{net}
		needs, err := s.Needs()
		if err != nil {
			return nil, err
		}
		for _, n := range needs {
			req = append(req, n.Unit())
		}
		groups := ""
		switch {
		case s.Or("accel", "") == "kvm":
			groups = "SupplementaryGroups=kvm\n"
		case s.Kind == "container":
			groups = "SupplementaryGroups=docker\n"
		}
		if s.Kind == "container" {
			req = append(req, "docker.service")
		}
		r := strings.Join(req, " ")
		us = append(us, unit{s.Unit(), fmt.Sprintf(unitTemplate,
			fmt.Sprintf("%s %s (%s %s)", lab.Prefix, s.Name, s.Kind, s.Or("ip", "")), r, r,
			user, group, groups, lab.Dir, run, s.Name, run, s.Name, run, s.Name)})
	}
	return us, nil
}

func cmdUnits(x *ctx) error {
	us, err := x.units()
	if err != nil {
		return err
	}
	if !x.install {
		for _, u := range us {
			x.printf("# /etc/systemd/system/%s\n%s\n", u.name, u.text)
		}
		return nil
	}
	if x.e.euid != 0 {
		return fmt.Errorf("units -install needs root: sudo %s", x.again("units -install"))
	}
	for _, u := range us {
		f := filepath.Join(x.e.systemd, u.name)
		if b, err := os.ReadFile(f); err == nil && string(b) == u.text {
			continue
		}
		if err := os.WriteFile(f, []byte(u.text), 0o644); err != nil {
			return err
		}
		if err := os.Chmod(f, 0o644); err != nil {
			return err
		}
		x.printf("wrote %s\n", f)
	}
	if _, err := x.runx(nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	var off []string
	for _, u := range us {
		if _, err := x.e.run(nil, "systemctl", "is-enabled", "-q", u.name); err != nil {
			off = append(off, u.name)
		}
	}
	if off != nil {
		x.printf("not enabled (systemctl enable --now ...): %s\n", strings.Join(off, " "))
	}
	return nil
}
