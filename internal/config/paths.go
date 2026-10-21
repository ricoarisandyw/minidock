// Package config resolves where MiniDock keeps its files on disk.
package config

import (
	"os"
	"path/filepath"
)

// DataDir returns the folder MiniDock uses for user data.
// MINIDOCK_HOME overrides the OS default (handy for tests and portable setups).
func DataDir() (string, error) {
	if dir := os.Getenv("MINIDOCK_HOME"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "minidock"), nil
}

// TemplatesDir returns the folder holding user-made templates.
func TemplatesDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "templates"), nil
}

// ExternalDir returns the folder holding saved connections to databases
// MiniDock did not create. The files hold passwords, so the folder is private.
func ExternalDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "external"), nil
}
