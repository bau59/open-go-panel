package docker

import (
	"reflect"
	"testing"
)

func TestNormalizeImageReference(t *testing.T) {
	tests := map[string]string{
		"nginx":                                      "nginx:latest",
		"redis:7":                                    "redis:7",
		"ghcr.io/acme/app:stable":                    "ghcr.io/acme/app:stable",
		"https://ghcr.io/acme/app:stable":            "ghcr.io/acme/app:stable",
		"https://hub.docker.com/_/redis":             "redis:latest",
		"https://hub.docker.com/r/library/nginx":     "library/nginx:latest",
		"registry.example.com/team/app@sha256:abc123": "registry.example.com/team/app@sha256:abc123",
	}
	for input, want := range tests {
		if got := normalizeImageReference(input); got != want {
			t.Fatalf("normalizeImageReference(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPublishedPortArgs(t *testing.T) {
	tests := []struct {
		name   string
		ports  string
		public bool
		want   []string
		bad    bool
	}{
		{name: "none"},
		{name: "local", ports: "8080:80, 8443:443/tcp", want: []string{"-p", "127.0.0.1:8080:80", "-p", "127.0.0.1:8443:443/tcp"}},
		{name: "public opt-in", ports: "8080:80/udp", public: true, want: []string{"-p", "0.0.0.0:8080:80/udp"}},
		{name: "invalid single port", ports: "80", bad: true},
		{name: "invalid range", ports: "70000:80", bad: true},
		{name: "invalid empty mapping", ports: "8080:80,", bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := publishedPortArgs(tc.ports, tc.public)
			if (err != nil) != tc.bad {
				t.Fatalf("publishedPortArgs(%q): error=%v, bad=%v", tc.ports, err, tc.bad)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("publishedPortArgs(%q) = %#v, want %#v", tc.ports, got, tc.want)
			}
		})
	}
}
