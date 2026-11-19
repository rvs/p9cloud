//go:build !unix

package main

func waitsignal() string { return "" }
