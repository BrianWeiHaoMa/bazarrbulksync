package invocation

import "testing"

// TestDefaultStateFile: P1's default on each system, from the environment
// alone; XDG_CONFIG_HOME does not move Linux's ~/.config, and a value that
// is not an absolute path is not a home.
func TestDefaultStateFile(t *testing.T) {
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", map[string]string{"HOME": "/home/ana"}, "/home/ana/.config/bazarrbulksync/state.json"},
		{"linux", map[string]string{"HOME": "/home/ana/"}, "/home/ana/.config/bazarrbulksync/state.json"},
		{"linux", map[string]string{"HOME": "/home/ana", "XDG_CONFIG_HOME": "/cfg"}, "/home/ana/.config/bazarrbulksync/state.json"},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/cfg"}, ""},
		{"linux", map[string]string{"HOME": "home"}, ""},
		{"linux", nil, ""},
		{"darwin", map[string]string{"HOME": "/Users/ana", "XDG_CONFIG_HOME": "/cfg"}, "/Users/ana/Library/Application Support/bazarrbulksync/state.json"},
		{"darwin", nil, ""},
		{"windows", map[string]string{"AppData": `C:\Users\ana\AppData\Roaming`, "HOME": "/home/ana"}, `C:\Users\ana\AppData\Roaming\bazarrbulksync\state.json`},
		{"windows", map[string]string{"HOME": "/home/ana"}, ""},
	} {
		path, problem := defaultStateFile(tc.goos, func(name string) string { return tc.env[name] })
		if path != tc.want || (path == "") != (problem != "") {
			t.Errorf("%s %v: %q, %q; want %q", tc.goos, tc.env, path, problem, tc.want)
		}
	}
	StateFile = "/data/state.json"
	defer func() { StateFile = "" }()
	if path, _ := defaultStateFile("linux", func(string) string { return "" }); path != "/data/state.json" {
		t.Errorf("a build's default: %q, want /data/state.json", path)
	}
}
