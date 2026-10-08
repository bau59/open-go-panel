package docker

import (
	"strings"
	"testing"
)

func TestParseGitHubRepository(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		ok          bool
	}{
		{input: "https://github.com/team/private-project", want: "team/private-project", ok: true},
		{input: "https://github.com/team/private-project.git", want: "team/private-project", ok: true},
		{input: "git@github.com:team/private-project.git", want: "team/private-project", ok: true},
		{input: "team/private-project", want: "team/private-project", ok: true},
		{input: "https://evil.example/team/project", ok: false},
		{input: "https://github.com/team/project?access_token=secret", ok: false},
		{input: "git@evil.example:team/project", ok: false},
		{input: "https://github.com/team/project/tree/main", ok: false},
		{input: "team/../../etc", ok: false},
		{input: "team/project..other", ok: false},
		{input: "", ok: false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseGitHubRepository(tc.input)
			if (err == nil) != tc.ok {
				t.Fatalf("parseGitHubRepository(%q) = %v, %v", tc.input, got, err)
			}
			if tc.ok && got.ID() != tc.want {
				t.Fatalf("repository = %q, want %q", got.ID(), tc.want)
			}
		})
	}
}

func TestDockerGitHubBranchesAndDockerfiles(t *testing.T) {
	for _, branch := range []string{"", "main", "release/v1.2", "feature_docker"} {
		if err := validateBranch(branch); err != nil {
			t.Fatalf("valid branch %q rejected: %v", branch, err)
		}
	}
	for _, branch := range []string{"--upload-pack=evil", "../danger", "a..b", ".git/config", "main/", "foo//bar", "main.lock"} {
		if err := validateBranch(branch); err == nil {
			t.Fatalf("invalid branch %q accepted", branch)
		}
	}
	for _, name := range []string{"Dockerfile", "docker/Dockerfile", "build/Dockerfile.prod"} {
		if _, err := validateDockerfile(name); err != nil {
			t.Fatalf("valid Dockerfile %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"../Dockerfile", "/etc/passwd", ".", "dir/../../outside", "a\nDockerfile"} {
		if _, err := validateDockerfile(name); err == nil {
			t.Fatalf("invalid Dockerfile %q accepted", name)
		}
	}
}

func TestDockerBuildOutputIsBounded(t *testing.T) {
	var capture cappedBuildOutput
	data := strings.Repeat("build output ", 3000)
	n, err := capture.Write([]byte(data))
	if err != nil || n != len(data) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if len(capture.buf) != 8192 {
		t.Fatalf("captured %d bytes, want 8192", len(capture.buf))
	}
	if !strings.HasSuffix(string(capture.buf), data[len(data)-8192:]) {
		t.Fatal("output tail was not preserved")
	}
}
