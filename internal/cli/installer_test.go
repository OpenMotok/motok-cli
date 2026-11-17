package cli

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallerLocalArchiveAndChecksum(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX installer")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	temp := t.TempDir()
	binary := filepath.Join(temp, "motok")
	build := exec.Command("go", "build", "-o", binary, "./cmd/motok")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	archive := fmt.Sprintf("motok_v0.1.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archivePath := filepath.Join(temp, archive)
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zipped := gzip.NewWriter(file)
	packed := tar.NewWriter(zipped)
	if err = packed.WriteHeader(&tar.Header{Name: "motok", Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err = packed.Write(content); err != nil {
		t.Fatal(err)
	}
	for _, close := range []func() error{packed.Close, zipped.Close, file.Close} {
		if err = close(); err != nil {
			t.Fatal(err)
		}
	}
	compressed, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("%x  %s\n", sha256.Sum256(compressed), archive)
	if err = os.WriteFile(filepath.Join(temp, "SHA256SUMS"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(temp, "bin with spaces")
	install := func() ([]byte, error) {
		return exec.Command("sh", filepath.Join(root, "install.sh"), "--from-dir", temp, "--bin-dir", binDir, "--version", "v0.1.0").CombinedOutput()
	}
	if out, err := install(); err != nil {
		t.Fatalf("install: %v %s", err, out)
	}
	installed := filepath.Join(binDir, "motok")
	out, err := exec.Command(installed, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "0.1.0" {
		t.Fatal("installed binary failed", err)
	}
	before, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(archivePath, append(compressed, []byte("corruption")...), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := install(); err == nil || !strings.Contains(string(out), "Checksum mismatch") {
		t.Fatal("corrupt archive accepted")
	}
	after, err := os.ReadFile(installed)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("failed install replaced existing binary")
	}
}

func TestWindowsInstallerLocalArchiveAndChecksum(t *testing.T) {
	powershell, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell unavailable")
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin with spaces")
	archive := "motok_v0.1.0_windows_amd64.zip"
	pack := func(entry, content string) {
		t.Helper()
		file, err := os.Create(filepath.Join(temp, archive))
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(file)
		item, err := writer.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = item.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(temp, archive))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(temp, "SHA256SUMS"), []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(data), archive)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	install := func() ([]byte, error) {
		cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-File", filepath.Join(root, "install.ps1"), "-Version", "v0.1.0", "-FromDir", temp, "-BinDir", binDir, "-NoPathUpdate")
		cmd.Env = append(os.Environ(), "OS=Windows_NT", "PROCESSOR_ARCHITECTURE=AMD64", "PROCESSOR_ARCHITEW6432=AMD64")
		return cmd.CombinedOutput()
	}
	pack("motok.exe", "original binary fixture")
	if out, err := install(); err != nil {
		t.Fatalf("Windows fixture install: %v %s", err, out)
	}
	binary := filepath.Join(binDir, "motok.exe")
	original, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(temp, archive), []byte("corrupted archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := install(); err == nil || !strings.Contains(string(out), "Checksum mismatch") {
		t.Fatal("checksum failure not rejected", err, string(out))
	}
	after, err := os.ReadFile(binary)
	if err != nil || string(after) != string(original) {
		t.Fatal("checksum failure changed binary", err)
	}
	pack("../escape.exe", "bad fixture")
	if out, err := install(); err == nil || !strings.Contains(string(out), "Invalid binary archive") {
		t.Fatal("unsafe archive accepted", err, string(out))
	}
	pack("motok.exe", "replacement binary fixture")
	if out, err := install(); err != nil {
		t.Fatalf("Windows fixture upgrade: %v %s", err, out)
	}
	after, err = os.ReadFile(binary)
	if err != nil || string(after) != "replacement binary fixture" {
		t.Fatal("upgrade failed", err)
	}
}
