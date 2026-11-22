// Command release builds, on this machine, what a release of bazarrbulksync
// ships (F2): the six executables and the container image. Run from the
// repository root:
//
//	go run ./release <version>
//
// It writes release/out/bazarrbulksync-<os>-<arch> (".exe" on Windows) for
// linux, darwin, and windows on amd64 and arm64, and leaves the image
// wayhowma/bazarrbulksync:<version>, also tagged latest, for linux/amd64 and
// linux/arm64 in the local Docker. It publishes and pushes nothing.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// Image is the name the image is tagged with.
const Image = "wayhowma/bazarrbulksync"

// outDir is where the build writes, relative to the repository root;
// release/.gitignore keeps it out of Git. imageDir, inside it, is the image's
// whole build context.
const (
	outDir   = "release/out"
	imageDir = outDir + "/image"
)

// Platforms are the systems and architectures of the release executables;
// imagePlatforms those of the image.
var (
	Platforms = []Platform{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	}
	imagePlatforms = []Platform{{"linux", "amd64"}, {"linux", "arm64"}}
)

// Platform is one system and architecture, in Go's names.
type Platform struct{ OS, Arch string }

// Executable is the file name of the release executable for p.
func (p Platform) Executable() string {
	name := "bazarrbulksync-" + p.OS + "-" + p.Arch
	if p.OS == "windows" {
		name += ".exe"
	}
	return name
}

// ImageExecutable is the path, under imageDir, of the image's executable
// for p, as release/Dockerfile copies it.
func (p Platform) ImageExecutable() string {
	return p.OS + "-" + p.Arch + "/bazarrbulksync"
}

// ImageStateFile is the image's default state file (F9); the release
// executables keep P1's per-user path, which the program computes itself.
const ImageStateFile = "/data/state.json"

// A version is also an image tag, so it takes a tag's characters.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func main() {
	if len(os.Args) != 2 || !versionPattern.MatchString(os.Args[1]) {
		fmt.Fprintln(os.Stderr, "usage: go run ./release <version>  (letters, digits, '.', '_', '-'; for example 1.0.0)")
		os.Exit(2)
	}
	if _, err := os.Stat("go.mod"); err != nil {
		fmt.Fprintln(os.Stderr, "release: run from the repository root")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	start := time.Now()
	if err := build(ctx, os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "release: %v\n", err)
		os.Exit(1)
	}
	logf("built %s for %d platforms in %s, and the image %s:%s for linux/amd64 and linux/arm64, in %s",
		os.Args[1], len(Platforms), outDir, Image, os.Args[1], time.Since(start).Round(time.Second))
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, "release: "+format+"\n", args...) }

// build replaces whatever an earlier build left in outDir, then builds the
// executables and the image.
func build(ctx context.Context, version string) error {
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	for _, p := range Platforms {
		if err := goBuild(ctx, p, filepath.Join(outDir, p.Executable()), version, ""); err != nil {
			return err
		}
	}
	for _, p := range imagePlatforms {
		if err := goBuild(ctx, p, filepath.Join(imageDir, p.ImageExecutable()), version, ImageStateFile); err != nil {
			return err
		}
	}
	return buildImage(ctx, version)
}

// goBuild builds the program for p into out, with cgo off (R5, S6), stamped
// with version and, when stateFile is set, that default state file.
func goBuild(ctx context.Context, p Platform, out, version, stateFile string) error {
	const invocation = "github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	ldflags := "-s -w -X " + invocation + ".Version=" + version
	if stateFile != "" {
		ldflags += " -X " + invocation + ".StateFile=" + stateFile
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+p.OS, "GOARCH="+p.Arch)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building %s: %w", out, err)
	}
	logf("built %s", out)
	return nil
}

// buildImage builds the image for both platforms from imageDir into the
// local image store, which needs Docker's containerd image store to hold a
// multi-platform image.
func buildImage(ctx context.Context, version string) error {
	config, err := os.MkdirTemp("", "bazarrbulksync-release-docker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(config)
	cmd := exec.CommandContext(ctx, "docker", "build",
		"--platform", "linux/amd64,linux/arm64", "--provenance=false",
		"-f", "release/Dockerfile", "-t", Image+":"+version, "-t", Image+":latest", imageDir)
	// An empty client configuration: the base image is pulled anonymously,
	// no stored credential or credential helper is consulted, and nothing
	// can log in or push.
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+config)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building the image: %w", err)
	}
	return nil
}
