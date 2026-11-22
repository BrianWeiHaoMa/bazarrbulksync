// Command testbed stands up a real Bazarr with Sonarr and Radarr in the
// local Docker daemon, with a provisioned library of open-licensed media, and
// takes it down again. Run from the repository root:
//
//	go run ./testbed up    # ends once Bazarr's API shows every placed subtitle
//	go run ./testbed down  # removes everything but the downloaded media
//
// Up writes testbed/run/bed.json, which a check reads when the environment
// variable BAZARRBULKSYNC_TESTBED names it.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The images, each pinned to one release. bazarrImage is the one setting
// that chooses the Bazarr release the bed runs.
const (
	bazarrImage = "linuxserver/bazarr:1.6.2"
	sonarrImage = "linuxserver/sonarr:4.0.20"
	radarrImage = "linuxserver/radarr:6.4.4"
)

// Every Docker object the bed creates carries this prefix.
const prefix = "bazarrbulksync-testbed"

const (
	network         = prefix
	downloadsVolume = prefix + "-downloads" // kept by down
	mediaVolume     = prefix + "-media"
	// The container that places the library; named so that down can remove
	// one an interrupted up left running, which would hold the volumes.
	prepareContainer = prefix + "-prepare"
)

// A service of the bed.
type service struct {
	name      string
	image     string
	port      int    // where it listens in its container
	published int    // its port on this machine
	config    string // its configuration volume
}

var (
	bazarrService = service{"bazarr", bazarrImage, 6767, 6767, prefix + "-bazarr-config"}
	// A second Bazarr on the same library: the same media volume, Sonarr,
	// and Radarr, so it shows the same subtitles at the same paths, with its
	// own configuration volume, hence its own history and key.
	secondBazarrService = service{"bazarr2", bazarrImage, 6767, 6768, prefix + "-bazarr2-config"}
	sonarrService       = service{"sonarr", sonarrImage, 8989, 8989, prefix + "-sonarr-config"}
	radarrService       = service{"radarr", radarrImage, 7878, 7878, prefix + "-radarr-config"}
	services            = []service{bazarrService, secondBazarrService, sonarrService, radarrService}
)

func (s service) container() string { return prefix + "-" + s.name }

// url is the service's address from this machine; its port is published on
// localhost only.
func (s service) url() string { return fmt.Sprintf("http://localhost:%d", s.published) }

// manifestPath is where up writes the bed's file, relative to the
// repository root; testbed/.gitignore keeps it out of Git.
const manifestPath = "testbed/run/bed.json"

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		fmt.Fprintln(os.Stderr, "usage: go run ./testbed up|down")
		os.Exit(2)
	}
	if _, err := os.Stat("go.mod"); err != nil {
		fmt.Fprintln(os.Stderr, "testbed: run from the repository root")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	start := time.Now()
	var err error
	if os.Args[1] == "up" {
		err = up(ctx)
	} else {
		err = down(ctx)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "testbed %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
	logf("%s done in %s", os.Args[1], time.Since(start).Round(time.Second))
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, "testbed: "+format+"\n", args...) }

// down removes the bed's containers, network, and volumes, except the
// downloads, so that the next up starts from nothing without fetching the
// media again.
func down(ctx context.Context) error {
	containers := []string{prepareContainer}
	for _, s := range services {
		containers = append(containers, s.container())
	}
	for _, c := range containers {
		if err := docker(ctx, nil, "rm", "-f", "-v", c); err != nil {
			return err
		}
	}
	volumes := []string{mediaVolume}
	for _, s := range services {
		volumes = append(volumes, s.config)
	}
	if err := docker(ctx, nil, append([]string{"volume", "rm", "-f"}, volumes...)...); err != nil {
		return err
	}
	if out, err := dockerOutput(ctx, "network", "ls", "-q", "--filter", "name=^"+network+"$"); err != nil {
		return err
	} else if out != "" {
		if err := docker(ctx, nil, "network", "rm", network); err != nil {
			return err
		}
	}
	return os.RemoveAll(filepath.Dir(manifestPath))
}

// up takes down whatever is left of an earlier bed, then provisions a new one.
func up(ctx context.Context) error {
	if err := down(ctx); err != nil {
		return err
	}
	keys := map[string]string{}
	for _, s := range services {
		keys[s.name] = newKey()
	}
	if err := docker(ctx, nil, "network", "create", network); err != nil {
		return err
	}
	logf("placing the library (media is downloaded only once)")
	if err := docker(ctx, keyEnv(keys), prepareArgs()...); err != nil {
		return fmt.Errorf("placing the library: %w", err)
	}
	for _, s := range services {
		if err := docker(ctx, nil, "run", "-d", "--name", s.container(), "--network", network, "--network-alias", s.name,
			"-e", "PUID=911", "-e", "PGID=1001", "-e", "TZ=Etc/UTC",
			"-v", s.config+":/config", "-v", mediaVolume+":/media",
			"-p", fmt.Sprintf("127.0.0.1:%d:%d", s.published, s.port), s.image); err != nil {
			return err
		}
	}
	m, err := provision(ctx, keys)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		return err
	}
	if err := m.Write(manifestPath); err != nil {
		return err
	}
	logf("Bazarr answers at %s and %s; their addresses and API keys are in %s", m.URL, m.Second.URL, manifestPath)
	return nil
}

// prepareArgs runs prepareScript in the Bazarr image, then writes each
// service's configuration, with its key, before the service first starts.
// The keys pass from the docker command's environment (keyEnv) into the
// container's by name, so that no command line carries them.
func prepareArgs() []string {
	args := []string{"run", "--rm", "--name", prepareContainer, "--entrypoint", "sh",
		"-v", downloadsVolume + ":/downloads", "-v", mediaVolume + ":/media"}
	for _, s := range services {
		args = append(args, "-v", s.config+":/config-"+s.name, "-e", keyVar(s))
	}
	return append(args, bazarrImage, "-c", prepareScript()+configScript())
}

// keyVar names the variable that carries s's key into the prepare container.
func keyVar(s service) string { return strings.ToUpper(s.name) + "_KEY" }

// keyEnv is this process's environment with each service's key added.
func keyEnv(keys map[string]string) []string {
	env := os.Environ()
	for _, s := range services {
		env = append(env, keyVar(s)+"="+keys[s.name])
	}
	return env
}

// configScript writes the services' first configuration: Sonarr's and
// Radarr's API keys, and each Bazarr's key and its links to both. Bazarr's
// embedded_subs_show_desired is off because, left on, its API drops every
// embedded track of a video without a language profile (api/utils.py at
// v1.6.2), and the embedded track is there to show the client leaves it out.
// The keys are quoted, since YAML reads a key of decimal digits alone as a
// number, which Bazarr rejects.
func configScript() string {
	arr := func(s service) string {
		return fmt.Sprintf("cat > /config-%s/config.xml <<END\n<Config>\n  <ApiKey>$%s</ApiKey>\n"+
			"  <Port>%d</Port>\n  <BindAddress>*</BindAddress>\n  <UrlBase></UrlBase>\n"+
			"  <AuthenticationMethod>External</AuthenticationMethod>\n"+
			"  <AuthenticationRequired>DisabledForLocalAddresses</AuthenticationRequired>\n"+
			"  <AnalyticsEnabled>False</AnalyticsEnabled>\n  <UpdateMechanism>Docker</UpdateMechanism>\n"+
			"  <LogLevel>info</LogLevel>\n</Config>\nEND\n", s.name, keyVar(s), s.port)
	}
	bazarr := func(s service) string {
		return fmt.Sprintf(`mkdir -p /config-%[1]s/config
cat > /config-%[1]s/config/config.yaml <<END
auth:
  apikey: "$%[2]s"
general:
  use_sonarr: true
  use_radarr: true
  embedded_subs_show_desired: false
sonarr:
  ip: sonarr
  port: %[3]d
  apikey: "$SONARR_KEY"
radarr:
  ip: radarr
  port: %[4]d
  apikey: "$RADARR_KEY"
END
`, s.name, keyVar(s), sonarrService.port, radarrService.port)
	}
	return arr(sonarrService) + arr(radarrService) + bazarr(bazarrService) + bazarr(secondBazarrService)
}

// newKey returns a new API key: 32 lowercase hexadecimal digits, the form
// all three services generate themselves.
func newKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// docker runs a docker command with env as its environment (nil: this
// process's), passing its standard error through.
func docker(ctx context.Context, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = env
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", args[0], err)
	}
	return nil
}

func dockerOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}
