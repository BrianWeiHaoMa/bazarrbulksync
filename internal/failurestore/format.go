package failurestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// The state file is UTF-8 JSON, one object with exactly two members:
// "bazarrbulksync_state": 1, and "servers", an object keyed by address
// whose values are objects keyed by subtitle path, each value an array of
// event identities. A file of any other shape is not a state file, and the
// decoder is strict so that no other file is taken for an empty state and
// then overwritten.

const (
	versionMember = "bazarrbulksync_state"
	serversMember = "servers"
	version       = "1"
)

// encode is the state file's content for servers.
func encode(servers map[string]map[string][]string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	// Maps are written in key order, which puts the version first; an
	// array with no identities is [].
	if err := enc.Encode(map[string]any{versionMember: json.Number(version), serversMember: servers}); err != nil {
		panic(err) // strings, maps, and slices always encode
	}
	return b.Bytes()
}

// decode reads a state file's content, or says why it is not a state file.
func decode(b []byte) (map[string]map[string][]string, error) {
	if !utf8.Valid(b) {
		return nil, errors.New("not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var servers map[string]map[string][]string
	versioned := false
	err := object(d, func(name string) error {
		switch name {
		case versionMember:
			t, err := d.Token()
			if err != nil {
				return err
			}
			if n, ok := t.(json.Number); !ok || n != version {
				return fmt.Errorf("%q is %v, not %s", versionMember, t, version)
			}
			versioned = true
			return nil
		case serversMember:
			servers = map[string]map[string][]string{}
			return object(d, func(address string) error {
				if strings.HasSuffix(address, "/") {
					return fmt.Errorf("address %q ends in a slash", address)
				}
				records := map[string][]string{}
				servers[address] = records
				return object(d, func(path string) error {
					ids, err := identities(d)
					records[path] = ids
					return err
				})
			})
		default:
			return fmt.Errorf("unexpected member %q", name)
		}
	})
	if err != nil {
		return nil, err
	}
	if !versioned || servers == nil {
		return nil, fmt.Errorf("needs both %q and %q", versionMember, serversMember)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("more after the state's object")
	}
	return servers, nil
}

// object reads one JSON object, calling member for each member's name with
// the decoder before its value; a repeated name is an error.
func object(d *json.Decoder, member func(name string) error) error {
	if err := delim(d, '{'); err != nil {
		return err
	}
	seen := map[string]bool{}
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return err
		}
		name := t.(string) // inside an object the decoder yields only names here
		if seen[name] {
			return fmt.Errorf("member %q repeated", name)
		}
		seen[name] = true
		if err := member(name); err != nil {
			return err
		}
	}
	return delim(d, '}')
}

// identities reads one array of strings, possibly empty.
func identities(d *json.Decoder) ([]string, error) {
	if err := delim(d, '['); err != nil {
		return nil, err
	}
	ids := []string{}
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		id, ok := t.(string)
		if !ok {
			return nil, fmt.Errorf("identity %v is not a string", t)
		}
		ids = append(ids, id)
	}
	return ids, delim(d, ']')
}

func delim(d *json.Decoder, want json.Delim) error {
	t, err := d.Token()
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	if err != nil {
		return err
	}
	if t != want {
		return fmt.Errorf("found %v where %v belongs", t, want)
	}
	return nil
}
