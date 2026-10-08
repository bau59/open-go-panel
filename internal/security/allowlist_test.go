package security

import (
 "errors"
 "testing"
)

func TestIsMissingPanelAllowlist(t *testing.T) {
 cases:=[]struct{message string; missing bool}{
  {"cscli allowlists inspect open-go-panel --output json: exit status 1: Error: cscli allowlists inspect: unable to get allowlist: API error: allowlist 'open-go-panel' not found",true},
  {"API error: allowlist \"open-go-panel\" not found",true},
  {"API error: allowlist 'some-other-list' not found",false},
  {"permission denied",false},
  {"CrowdSec is not installed",false},
  {"connection refused",false},
 }
 for _,tc:=range cases {
  if got:=isMissingPanelAllowlist(errors.New(tc.message));got!=tc.missing {
   t.Errorf("missing %q = %v, want %v",tc.message,got,tc.missing)
  }
 }
 if isMissingPanelAllowlist(nil){t.Fatal("nil error incorrectly considered missing")}
}
