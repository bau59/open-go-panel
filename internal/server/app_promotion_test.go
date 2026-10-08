package server

import (
	"strings"
	"testing"
)

func TestGoRunModesDistinguishDevelopmentFromProduction(t *testing.T) {
	dev:=runModeOptions("go","go-air")
	if !strings.Contains(dev,`value="go-air" selected`) ||
		!strings.Contains(dev,"development") {
		t.Fatalf("Go development mode missing: %s",dev)
	}
	prod:=runModeOptions("go","go-binary")
	if !strings.Contains(prod,`value="go-binary" selected`) ||
		!strings.Contains(prod,"prebuilt production binary") {
		t.Fatalf("prebuilt production mode missing: %s",prod)
	}
	if strings.Contains(runModeOptions("node","node-npm"),`go-binary`) {
		t.Fatal("Go production mode exposed for Node applications")
	}
}
