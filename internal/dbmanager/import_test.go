package dbmanager

import "testing"

func TestParseRemoteConnection(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		engine   string
		host     string
		port     string
		database string
	}{
		{
			name: "mysql url",
			input: "mysql://user:secret@example.com:3307/app",
			engine: "mysql", host: "example.com", port: "3307", database: "app",
		},
		{
			name: "mysql go dsn",
			input: "user:secret@tcp(10.0.0.2:3306)/app",
			engine: "mysql", host: "10.0.0.2", port: "3306", database: "app",
		},
		{
			name: "postgres url",
			input: "postgres://user:secret@example.com:5433/app?sslmode=require",
			engine: "postgres", host: "example.com", port: "5433", database: "app",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRemoteConnection(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Engine != tt.engine || got.Host != tt.host || got.Port != tt.port || got.Database != tt.database {
				t.Fatalf("unexpected parse result: %#v", got)
			}
		})
	}
}

func TestParseRemoteConnectionRejectsUnsupported(t *testing.T) {
	if _, err := parseRemoteConnection("server=localhost;database=app"); err == nil {
		t.Fatal("expected unsupported connection format to fail")
	}
}
