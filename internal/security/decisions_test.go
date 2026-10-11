package security

import (
	"encoding/json"
	"testing"
)

func TestParseDecisionRecordsNestedCscliJSON(t *testing.T) {
	const payload = `[
		{
			"id": 169,
			"scenario": "crowdsecurity/http-crawl-non_statics",
			"source": {"cn": "CA", "as_name": "OVH SAS"},
			"decisions": [
				{"id": 405110, "origin": "crowdsec", "scope": "Ip",
				"value": "158.69.55.148", "type": "ban", "duration": "3h53m51s"}
			]
		},
		{
			"id": 168,
			"scenario": "crowdsecurity/http-probing",
			"source": {"cn": "US", "as_name": "Railnet LLC"},
			"decisions": [
				{"id": 405109, "origin": "crowdsec", "scope": "Ip",
				"value": "130.12.180.117", "type": "ban", "duration": "3h52m36s"}
			]
		},
		{"id": 167, "scenario": "crowdsecurity/ssh-time-based-bf_user-enum", "decisions": []}
	]`
	var raw any
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatal(err)
	}
	got := parseDecisionRecords(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 active decisions, got %d: %+v", len(got), got)
	}
	if got[0].ID != "405110" || got[0].Value != "158.69.55.148" ||
		got[0].Reason != "crowdsecurity/http-crawl-non_statics" ||
		got[0].Country != "CA" || got[0].AS != "OVH SAS" ||
		got[0].AlertID != "169" || got[0].Expiration != "3h53m51s" ||
		got[0].Action != "ban" || got[0].Source != "crowdsec" {
		t.Fatalf("nested decision not parsed correctly: %+v", got[0])
	}
	if got[1].Value != "130.12.180.117" || got[1].Reason != "crowdsecurity/http-probing" {
		t.Fatalf("second decision missing: %+v", got[1])
	}
}

func TestParseDecisionRecordsSupportsFlatAndEmptyResponses(t *testing.T) {
	for _, tc := range []struct{
		name string
		input string
		count int
	}{
		{"flat array", `[{"id": 4, "value":"203.0.113.1", "scenario":"manual", "type":"ban"}]`, 1},
		{"wrapped flat", `{"decisions":[{"value":"203.0.113.2","reason":"manual"}]}`, 1},
		{"wrapped nested", `{"items":[{"scenario":"ssh-bf","decisions":[{"value":"203.0.113.3"}]}]}`, 1},
		{"empty", `[]`, 0},
		{"null", `null`, 0},
		{"alert only", `[{"scenario":"ssh-bf","source":{"value":"203.0.113.4"},"decisions":[]}]`, 0},
	}{
		t.Run(tc.name, func(t *testing.T) {
			var raw any
			if err := json.Unmarshal([]byte(tc.input), &raw); err != nil { t.Fatal(err) }
			got := parseDecisionRecords(raw)
			if len(got) != tc.count { t.Fatalf("count=%d, want %d: %+v", len(got), tc.count, got) }
		})
	}
}
