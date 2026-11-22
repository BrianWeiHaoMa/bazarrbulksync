package main

import (
	"bytes"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ReleaseVar names the version a build was given; set, it lets these checks
// read the build's outputs in release/out:
//
//	go run ./release 1.0.0 && BAZARRBULKSYNC_RELEASE=1.0.0 go test -count=1 ./release
const ReleaseVar = "BAZARRBULKSYNC_RELEASE"

// built returns the version the build was given, skipping without one.
func built(t *testing.T) string {
	t.Helper()
	v := os.Getenv(ReleaseVar)
	if v == "" {
		t.Skipf("no release build to check: run `go run ./release <version>`, then set %s=<version>", ReleaseVar)
	}
	return v
}

// out is the path of a build output; the test runs in release/.
func out(name string) string { return filepath.Join("out", name) }

// F2's six executables and the image's two, listed here rather than taken
// from the build's own lists, so that a platform dropped there is missed here.
var artifacts = []struct {
	name  string
	p     Platform
	image bool
}{
	{"bazarrbulksync-linux-amd64", Platform{"linux", "amd64"}, false},
	{"bazarrbulksync-linux-arm64", Platform{"linux", "arm64"}, false},
	{"bazarrbulksync-darwin-amd64", Platform{"darwin", "amd64"}, false},
	{"bazarrbulksync-darwin-arm64", Platform{"darwin", "arm64"}, false},
	{"bazarrbulksync-windows-amd64.exe", Platform{"windows", "amd64"}, false},
	{"bazarrbulksync-windows-arm64.exe", Platform{"windows", "arm64"}, false},
	{"image/linux-amd64/bazarrbulksync", Platform{"linux", "amd64"}, true},
	{"image/linux-arm64/bazarrbulksync", Platform{"linux", "arm64"}, true},
}

func TestArtifacts(t *testing.T) {
	built(t)
	for _, a := range artifacts {
		path := out(a.name)
		t.Run(path, func(t *testing.T) {
			if got := header(t, path); got != a.p {
				t.Errorf("the file's header says %s/%s, want %s/%s (F2)", got.OS, got.Arch, a.p.OS, a.p.Arch)
			}
			info, err := buildinfo.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			settings := map[string]string{}
			for _, s := range info.Settings {
				settings[s.Key] = s.Value
			}
			if settings["CGO_ENABLED"] != "0" || settings["GOOS"] != a.p.OS || settings["GOARCH"] != a.p.Arch {
				t.Errorf("built with CGO_ENABLED=%q GOOS=%q GOARCH=%q, want cgo off for %s/%s (R5)",
					settings["CGO_ENABLED"], settings["GOOS"], settings["GOARCH"], a.p.OS, a.p.Arch)
			}
			if !strings.Contains(","+settings["DefaultGODEBUG"]+",", ",x509sslcertoverrideplatform=0,") {
				t.Errorf("DefaultGODEBUG is %q, want x509sslcertoverrideplatform=0 (Foundations, S7)", settings["DefaultGODEBUG"])
			}
			// Most of these cannot run here, so the image's default state
			// file is looked for in each file (F9, Foundations).
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if holds := bytes.Contains(data, []byte(ImageStateFile)); holds != a.image {
				t.Errorf("holds %s: %v, want %v: only the image's executables default to it", ImageStateFile, holds, a.image)
			}
		})
	}
}

// header reads the system and architecture a file's own header gives, and
// for an ELF file requires it to need no program interpreter or shared
// library, so that it runs where no C library is installed (F2, R5).
func header(t *testing.T, path string) Platform {
	t.Helper()
	if f, err := elf.Open(path); err == nil {
		defer f.Close()
		for _, prog := range f.Progs {
			if prog.Type == elf.PT_INTERP {
				t.Errorf("names a program interpreter, so it needs a C library")
			}
		}
		if libs, err := f.ImportedLibraries(); err != nil || len(libs) > 0 {
			t.Errorf("needs the shared libraries %v (%v)", libs, err)
		}
		arch := map[elf.Machine]string{elf.EM_X86_64: "amd64", elf.EM_AARCH64: "arm64"}[f.Machine]
		return Platform{"linux", arch}
	}
	if f, err := macho.Open(path); err == nil {
		defer f.Close()
		return Platform{"darwin", map[macho.Cpu]string{macho.CpuAmd64: "amd64", macho.CpuArm64: "arm64"}[f.Cpu]}
	}
	if f, err := pe.Open(path); err == nil {
		defer f.Close()
		return Platform{"windows", map[uint16]string{pe.IMAGE_FILE_MACHINE_AMD64: "amd64", pe.IMAGE_FILE_MACHINE_ARM64: "arm64"}[f.Machine]}
	}
	t.Fatalf("%s is no ELF, Mach-O, or PE file", path)
	return Platform{}
}

// TestDefaults runs the linux/amd64 executables, the release one and the
// image's: each prints the version the build was given, and the default
// state file of where it runs (F5, F9, P1).
func TestDefaults(t *testing.T) {
	version := built(t)
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("only the linux/amd64 executables run on this machine")
	}
	home := t.TempDir()
	run := func(path string, args ...string) string {
		t.Helper()
		cmd := exec.Command(path, args...)
		cmd.Env = []string{"HOME=" + home}
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s %v: %v", path, args, err)
		}
		return string(got)
	}
	for _, path := range []string{out(Platforms[0].Executable()), out("image/" + imagePlatforms[0].ImageExecutable())} {
		if got := run(path, "--version"); got != "bazarrbulksync "+version+"\n" {
			t.Errorf("%s --version printed %q, want the version %s", path, got, version)
		}
	}
	p1 := filepath.Join(home, ".config", "bazarrbulksync", "state.json")
	if help := run(out(Platforms[0].Executable()), "sync", "--help"); !strings.Contains(help, "default state file is "+p1+"\n") {
		t.Errorf("the release executable's sync --help does not print P1's path %s:\n%s", p1, help)
	}
	if help := run(out("image/"+imagePlatforms[0].ImageExecutable()), "sync", "--help"); !strings.Contains(help, "default state file is "+ImageStateFile+"\n") {
		t.Errorf("the image's executable's sync --help does not print %s:\n%s", ImageStateFile, help)
	}
}

// TestIgnored: a build leaves no file Git would take as new (T2).
func TestIgnored(t *testing.T) {
	built(t)
	status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "--", "out").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(status) > 0 {
		t.Errorf("git status shows the build's outputs:\n%s", status)
	}
}
