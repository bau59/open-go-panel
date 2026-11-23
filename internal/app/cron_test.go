package app

import (
	"strings"
	"testing"
)

func TestCronCalendar(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"*/5 * * * *", []string{"*-*-* *:00,05,10,15,20,25,30,35,40,45,50,55:00"}},
		{"0 3 * * *", []string{"*-*-* 03:00:00"}},
		{"@hourly", []string{"*-*-* *:00:00"}},
		{"@weekly", []string{"Sun *-*-* 00:00:00"}},
		{"0 9 * * 1-5", []string{"Mon,Tue,Wed,Thu,Fri *-*-* 09:00:00"}},
		{"0 9 1 * 1", []string{"*-*-01 09:00:00", "Mon *-*-* 09:00:00"}},
		{"15 2 1 1 *", []string{"*-01-01 02:15:00"}},
		{"0 0 * * 7", []string{"Sun *-*-* 00:00:00"}},
	}
	for _, tc := range tests {
		actual, err := cronCalendar(tc.input)
		if err != nil {
			t.Errorf("%q: %v", tc.input, err)
			continue
		}
		if len(actual) != len(tc.want) {
			t.Errorf("%q: %v, want %v", tc.input, actual, tc.want)
			continue
		}
		for i := range actual {
			if actual[i] != tc.want[i] {
				t.Errorf("%q: calendar %d = %q, want %q", tc.input, i, actual[i], tc.want[i])
			}
		}
	}
}

func TestCronCalendarRejectsBadFields(t *testing.T) {
	for _, input := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *",
		"*/0 * * * *", "1/100 * * * *", "* 24 * * *",
		"* * 0 * *", "* * * 13 *", "* * * * 8",
		"* * * * 4-2", "1,,2 * * * *", "5/2/3 * * * *",
		"@reboot",
	} {
		if got, err := cronCalendar(input); err == nil {
			t.Errorf("accepted invalid cron %q: %v", input, got)
		}
	}
}

func TestCronRunnerUsesUnprivilegedUser(t *testing.T) {
	app := App{ID: 7, User: "webuser", Root: "/home/webuser/apps/site", Type: "go"}
	job := CronJob{ID: 8, Command: "echo 'hello' && touch done"}
	runner := cronRunner(app, job)
	if !strings.Contains(runner, "runuser -u 'webuser' -- /bin/bash -c") {
		t.Fatalf("runner does not switch to app user: %s", runner)
	}
	if !strings.Contains(runner, "'\"'\"'") {
		t.Fatalf("runner must correctly quote single quotes: %s", runner)
	}
}

func TestCronRunnerPHP74ContainerOwnership(t *testing.T) {
	app := App{ID: 9, User: "webuser", Root: "/home/webuser/apps/legacy", Type: "php",
		Service: ServiceConfig{PHPVersion: "7.4"}}
	runner := cronRunner(app, CronJob{ID: 13, Command: "php cron.php"})
	for _, required := range []string{
		"docker inspect", "org.open-go-panel.managed-runtime", "org.open-go-panel.php-app-id",
		"php74:9", "docker exec --workdir", "ogp-php74-9", "/bin/sh -lc 'php cron.php'",
	} {
		if !strings.Contains(runner, required) {
			t.Errorf("PHP 7.4 runner missing %q", required)
		}
	}
}
