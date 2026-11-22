package main_test

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"debug/buildinfo"
	"debug/elf"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// The image's checks: `go run ./release <version>`, a fresh bed, then
//
//	BAZARRBULKSYNC_TESTBED=$PWD/testbed/run/bed.json BAZARRBULKSYNC_TESTBED_IMAGE=1 \
//	BAZARRBULKSYNC_RELEASE=<version> go test -count=1 . -run TestBedImage
//
// The stop check leaves a sync to Bazarr, which changes the bed.
const (
	imageVar = "BAZARRBULKSYNC_TESTBED_IMAGE"
	// releaseVar holds the version the release build was given, as for
	// release/release_test.go.
	releaseVar = "BAZARRBULKSYNC_RELEASE"
	// releaseImage is the image the release build leaves, named as `In use`
	// runs it.
	releaseImage = "wayhowma/bazarrbulksync"
	// releaseLinux is the linux/amd64 release executable.
	releaseLinux = "release/out/bazarrbulksync-linux-amd64"
	// checkPrefix names every container and volume these checks make.
	checkPrefix = "bazarrbulksync-imagecheck-"
)

func TestBedImage(t *testing.T) {
	m := changingBed(t, imageVar)
	version := os.Getenv(releaseVar)
	if version == "" {
		t.Fatalf("set %s to the version `go run ./release <version>` was given", releaseVar)
	}
	// The image runs here, and its state file is copied out to be read here.
	if os.Getenv(sshVar) != "" {
		t.Fatalf("the image's checks run the image in this machine's Docker: unset %s", sshVar)
	}
	b := newChangeBed(t, m)

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run("contents "+arch, func(t *testing.T) { imageContents(t, arch) })
	}

	t.Run("version and state file", func(t *testing.T) {
		if r, _ := b.dockerRun(t, "", "--rm", releaseImage, "--version"); r.code != 0 || r.stdout != "bazarrbulksync "+version+"\n" {
			t.Errorf("--version: code %d, stdout %q, stderr %q; want the version %s (F5)", r.code, r.stdout, r.stderr, version)
		}
		tagged := b.docker(t, "", "image", "inspect", "-f", "{{.Id}}", releaseImage+":"+version, releaseImage+":latest")
		if ids := strings.Fields(tagged.stdout); tagged.code != 0 || len(ids) != 2 || ids[0] != ids[1] {
			t.Errorf("%s:%s and :latest are %q (%s); want the one image the build made", releaseImage, version, tagged.stdout, tagged.stderr)
		}
		r, _ := b.dockerRun(t, "", "--rm", releaseImage, "sync", "--help")
		if r.code != 0 || !strings.Contains(r.stdout, "default state file is /data/state.json\n") {
			t.Errorf("sync --help: code %d, stdout %q; want /data/state.json as the default (F9)", r.code, r.stdout)
		}
	})

	// The file a user downloads, in a container whose image holds no C
	// library, shell, or runtime (contents above): F2's "requires no
	// interpreter, runtime, or other installed software".
	t.Run("release executable alone", func(t *testing.T) {
		exe, err := filepath.Abs(releaseLinux)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(exe); err != nil {
			t.Fatalf("%v; run `go run ./release %s`", err, version)
		}
		got, _ := b.dockerRun(t, m.URL, "--rm", "--network", "host", "-v", exe+":/release:ro",
			"--entrypoint", "/release", "-e", "BAZARR_URL", "-e", "BAZARR_API_KEY", releaseImage, "list")
		want := runExecutable(t, m.URL, m.APIKey, "list")
		if got.code != 0 || got.stderr != "" || got.stdout != want.stdout {
			t.Errorf("code %d, stderr %q, stdout\n%s\nwant exit 0 and what the executable prints here:\n%s", got.code, got.stderr, got.stdout, want.stdout)
		}
	})

	// The first Done-when check's second sentence, run as it is written but
	// for reaching the bed, whose port is published on this machine's
	// localhost, and for never pulling an image of that name from a registry.
	t.Run("list --json", func(t *testing.T) {
		r, _ := b.dockerRun(t, m.URL, "--rm", "--network", "host",
			"-e", "BAZARR_URL", "-e", "BAZARR_API_KEY", releaseImage, "list", "--json")
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("code %d, stderr %q", r.code, r.stderr)
		}
		var printed []bazarr.Subtitle
		for _, obj := range decodeObjects(t, r.stdout) {
			s, ok := placedAs(m, obj)
			if !ok {
				t.Fatalf("printed %+v, which is not F7's object of a subtitle the bed placed", obj)
			}
			printed = append(printed, s)
		}
		if shown := pathsInAnswer(t, m); !samePaths(paths(printed), shown) || !sameSubtitles(printed, m.Subtitles) {
			t.Errorf("objects for %v, want one for each path Bazarr shows %v", paths(printed), shown)
		}
	})

	// In a container the image is the machine that must trust Bazarr's
	// certificate (E1, S7). Docker Hub's registry, which the build pulls
	// from, is signed by a public authority; it answers the connection check
	// with something other than Bazarr's status.
	t.Run("https", func(t *testing.T) {
		const address = "https://registry-1.docker.io"
		r, _ := b.dockerRun(t, "", "--rm", "-e", "BAZARR_URL="+address,
			"-e", "BAZARR_API_KEY=0123456789abcdef0123456789abcdef", releaseImage, "list")
		switch {
		case strings.Contains(r.stderr, "x509") || strings.Contains(r.stderr, "certificate"):
			t.Errorf("the connection check failed on the certificate: %s", r.stderr)
		case strings.Contains(r.stderr, "no such host") || strings.Contains(r.stderr, "dial tcp"):
			t.Logf("no public HTTPS server reachable (%s); the bundle's certificates parsed in the contents checks", strings.TrimSpace(r.stderr))
		case r.code == 0 || !strings.Contains(r.stderr, address):
			t.Errorf("code %d, stderr %q; want the connection check to fail naming %s", r.code, r.stderr, address)
		default:
			t.Logf("the connection check got past the certificate: %s", strings.TrimSpace(r.stderr))
		}
	})

	t.Run("nightly sync", func(t *testing.T) { b.nightlySync(t) })
	t.Run("container stop", func(t *testing.T) { b.containerStop(t) })
}

// imageContents: each layer of the image for linux/<arch> holds the
// executable for that architecture and the CA bundle, and nothing else; the
// executable is the entry point in exec form, with no default arguments.
func imageContents(t *testing.T, arch string) {
	img := saveImage(t, releaseImage, arch)
	if img.OS != "linux" || img.Architecture != arch {
		t.Errorf("the configuration says %s/%s, want linux/%s", img.OS, img.Architecture, arch)
	}
	if !slices.Equal(img.Config.Entrypoint, []string{"/bazarrbulksync"}) || len(img.Config.Cmd) != 0 {
		t.Errorf("entry point %q, default arguments %q; want the executable alone as the entry point (F2, S8)", img.Config.Entrypoint, img.Config.Cmd)
	}
	// The bundle's path is the first of Go's Linux certificate files
	// (crypto/x509/root_linux.go certFiles).
	const exe, bundle = "bazarrbulksync", "etc/ssl/certs/ca-certificates.crt"
	dirs := []string{"etc", "etc/ssl", "etc/ssl/certs"}
	files := map[string][]byte{}
	for i, layer := range img.layers {
		for name, e := range layer {
			switch {
			case e.dir && slices.Contains(dirs, name):
			case !e.dir && (name == exe || name == bundle) && e.data != nil:
				files[name] = e.data
				if name == exe && e.mode&0o111 == 0 {
					t.Errorf("layer %d holds the executable with mode %o, which no user may run", i, e.mode)
				}
			default:
				t.Errorf("layer %d holds %q (type %q), which is neither the executable nor the CA bundle", i, name, e.kind)
			}
		}
	}
	f, err := elf.NewFile(bytes.NewReader(files[exe]))
	if err != nil {
		t.Fatalf("the executable: %v", err)
	}
	if want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[arch]; f.Machine != want {
		t.Errorf("the executable is for %v, want %v", f.Machine, want)
	}
	info, err := buildinfo.Read(bytes.NewReader(files[exe]))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(info.Settings, func(s debug.BuildSetting) bool { return s.Key == "CGO_ENABLED" && s.Value == "0" }) {
		t.Errorf("the executable's build information does not show cgo off (R5): %v", info.Settings)
	}
	// It cannot run here, so its default state file is looked for in it (F9).
	if !bytes.Contains(files[exe], []byte("/data/state.json")) {
		t.Errorf("the executable does not hold /data/state.json, the image's default state file")
	}
	certs, rest := 0, files[bundle]
	for {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			break
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			t.Errorf("a certificate of the bundle: %v", err)
		}
		certs++
	}
	if certs < 10 || len(bytes.TrimSpace(rest)) > 0 {
		t.Errorf("the bundle holds %d certificates and %d bytes besides; want a CA bundle (S7)", certs, len(rest))
	}
}

// nightlySync runs the nightly line of `In use` twice with one fresh named
// volume at /data, on the failure of the third Done-when check (the one
// German movie subtitle against an audio track its video lacks): the first
// container records it in /data/state.json, the second skips it (F9, G21).
func (b *changeBed) nightlySync(t *testing.T) {
	de := b.selected(t, "--language", "de", "--movies")
	if len(de) != 1 {
		t.Fatalf("the bed placed %d German movie subtitles, want 1", len(de))
	}
	sub := de[0]
	b.needNoSync(t, sub)
	line := b.listLine(t, sub)
	volume := checkName()
	if r := b.docker(t, "", "volume", "create", volume); r.code != 0 {
		t.Fatalf("creating a volume: %s", r.stderr)
	}
	t.Cleanup(func() {
		if r := b.docker(t, "", "volume", "rm", "-f", volume); r.code != 0 {
			t.Errorf("removing the volume %s: %s", volume, r.stderr)
		}
	})
	args := []string{"--rm", "--network", "host", "-e", "BAZARR_URL", "-e", "BAZARR_API_KEY",
		"-v", volume + ":/data", releaseImage, "sync", "--yes", "--language", "de", "--movies", "--reference", "a:5"}

	b.forget()
	r, _ := b.dockerRun(t, b.proxy, args...)
	syncs := b.syncsOfLastRun()
	if len(syncs) != 1 || syncs[0].status != 204 || syncs[0].query.Get("path") != sub.Path {
		t.Fatalf("the run sent %v; want one sync of %s, accepted with 204", describe(syncs), sub.Path)
	}
	b.syncRequest(t, syncs[0], map[string]string{"reference": "a:5"})
	if events := b.events(t, sub)[sub.Path]; len(events) > 0 {
		t.Errorf("Bazarr's history holds a sync of %s at %v; want the sync to have failed", sub.Path, events)
	}
	if r.code == 0 || !strings.HasPrefix(r.stdout, "failed  "+line) || strings.Contains(r.stderr, "state file") {
		t.Errorf("code %d, stdout %q, stderr %q; want %s reported failed, nonzero, and nothing said of the state file", r.code, r.stdout, r.stderr, sub.Path)
	}
	state := filepath.Join(t.TempDir(), "state.json")
	b.copyOut(t, volume, "/data/state.json", state)
	if !b.recorded(t, state, b.proxy, sub.Path) {
		t.Fatalf("/data/state.json in the volume holds no failure for %s under %s", sub.Path, b.proxy)
	}

	b.forget()
	r, _ = b.dockerRun(t, b.proxy, args...)
	if r.code != 0 || r.stdout != "\nTotal: 1 of 1 selected subtitle\nskipped (earlier failure) 1\n" {
		t.Errorf("the second container: code %d, stdout %q, stderr %q; want it skipped for its earlier failure, exit zero", r.code, r.stdout, r.stderr)
	}
	b.sends(t, 0)
}

// containerStop stops, with `docker stop`, a container whose `sync` run has
// a sync in Bazarr: the tool, process 1, gets the SIGTERM itself, prints its
// totals, and exits with its own code well inside the grace period (G4, S8).
func (b *changeBed) containerStop(t *testing.T) {
	sub := b.one(t, "the English subtitle of Sintel", func(s bazarr.Subtitle) bool {
		return s.Type == bazarr.TypeMovie && s.Title == "Sintel" && s.Language == "en" && !s.HI && !s.Forced
	})
	b.needNoSync(t, sub)
	selection := b.selectAlone(t, sub)
	q := watchQueue(t, b.Manifest)
	from := q.mark()
	args := append([]string{"-d", "--network", "host", "-e", "BAZARR_URL", "-e", "BAZARR_API_KEY", releaseImage, "sync", "--yes"}, selection...)
	b.forget()
	r, name := b.dockerRun(t, b.proxy, args...)
	if r.code != 0 {
		t.Fatalf("starting the container: code %d, stderr %q", r.code, r.stderr)
	}
	b.awaitSyncs(t, 1)
	if r := b.docker(t, "", "inspect", "-f", "{{.State.Running}}", name); strings.TrimSpace(r.stdout) != "true" {
		t.Fatalf("the run ended before the stop: %q %q", r.stdout, r.stderr)
	}
	const grace = 30
	sent := time.Now()
	if r := b.docker(t, "", "stop", "-t", strconv.Itoa(grace), name); r.code != 0 {
		t.Fatalf("docker stop: %s", r.stderr)
	}
	took := time.Since(sent)
	if !q.sawActive(from, sub.Path) {
		t.Errorf("Bazarr's queue never showed the run's sync of %s", sub.Path)
	}
	code := strings.TrimSpace(b.docker(t, "", "inspect", "-f", "{{.State.ExitCode}}", name).stdout)
	logs := b.docker(t, "", "logs", name)
	if strings.Contains(logs.stdout+logs.stderr, b.APIKey) {
		t.Fatalf("the run printed the API key")
	}
	// Following the sync to its end takes at least two queue reads five
	// seconds apart: a run that waited for it would take longer.
	if took > 5*time.Second || code != "1" {
		t.Errorf("the stop took %s and the exit code is %s; want the tool's own code 1, not 137, within 5s of a %ds grace period", took, code, grace)
	}
	t.Logf("the stop took %s", took)
	if logs.stdout != "\nTotal: 0 of 1 selected subtitle\n" {
		t.Errorf("standard output %q, standard error %q; want only the totals, no line for the sync left in Bazarr (G4)", logs.stdout, logs.stderr)
	}
	b.sends(t, 1)
	if r := b.docker(t, "", "cp", name+":/data/state.json", filepath.Join(t.TempDir(), "state.json")); r.code == 0 {
		t.Errorf("the stopped run left a state file; want the sync left in Bazarr not recorded (G4)")
	}
}

// docker runs docker with args to its end. url, when set, is BAZARR_URL and
// the bed's key BAZARR_API_KEY in docker's environment, which `-e NAME`
// passes into the container by name, so no command line carries the key.
func (b *changeBed) docker(t *testing.T, url string, args ...string) result {
	t.Helper()
	cmd := exec.Command("docker", args...)
	cmd.Env = os.Environ()
	if url != "" {
		cmd.Env = append(cmd.Env, "BAZARR_URL="+url, "BAZARR_API_KEY="+b.APIKey)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(5*time.Minute, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()
	_ = cmd.Wait()
	r := result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.keyInURL || strings.Contains(r.stdout+r.stderr, b.APIKey) {
		t.Fatalf("docker %q put the API key in a URL or printed it", args)
	}
	return r
}

// dockerRun runs `docker run` with args, as docker does, in a container of
// a new name, which it returns; the container is removed when the test ends,
// should docker have left it. It never pulls an image: one of releaseImage's
// name may be published, and only the local build is meant.
func (b *changeBed) dockerRun(t *testing.T, url string, args ...string) (result, string) {
	t.Helper()
	name := checkName()
	t.Cleanup(func() {
		if r := b.docker(t, "", "rm", "-f", name); r.code != 0 {
			t.Errorf("removing the container %s: %s", name, r.stderr)
		}
	})
	return b.docker(t, url, append([]string{"run", "--pull", "never", "--name", name}, args...)...), name
}

// copyOut copies the file at path in volume to local, through a container
// that is created and never started, since the image holds no program to
// read a file with.
func (b *changeBed) copyOut(t *testing.T, volume, path, local string) {
	t.Helper()
	name := checkName()
	if r := b.docker(t, "", "create", "--pull", "never", "--name", name, "-v", volume+":/data", releaseImage); r.code != 0 {
		t.Fatalf("docker create: %s", r.stderr)
	}
	defer b.docker(t, "", "rm", name)
	if r := b.docker(t, "", "cp", name+":"+path, local); r.code != 0 {
		t.Fatalf("docker cp %s: %s", path, r.stderr)
	}
}

// checkName returns a new name for a container or volume of these checks.
func checkName() string {
	return checkPrefix + strings.ToLower(rand.Text()[:8])
}
