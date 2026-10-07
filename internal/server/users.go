package server

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/bau59/open-go-panel/internal/linuxuser"
)

func registerUserRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /users", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		users, err := cfg.Users.List(r.Context())
		if err != nil {
			cfg.Logger.Error("list users failed", "err", err)
			writeHTML(w, cfg.Logger, http.StatusInternalServerError, usersPage(nil, err.Error()))
			return
		}

		writeHTML(w, cfg.Logger, http.StatusOK, usersPage(users, ""))
	})))

	mux.Handle("POST /users", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		if err := cfg.Users.Create(r.Context(), strings.TrimSpace(r.FormValue("username")), r.FormValue("password")); err != nil {
			users, listErr := cfg.Users.List(r.Context())
			if listErr != nil {
				cfg.Logger.Error("list users after create failure failed", "err", listErr)
			}
			writeHTML(w, cfg.Logger, http.StatusBadRequest, usersPage(users, err.Error()))
			return
		}

		http.Redirect(w, r, "/users", http.StatusSeeOther)
	})))

	mux.Handle("POST /users/{username}/delete", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Users.Delete(r.Context(), r.PathValue("username")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/users", http.StatusSeeOther)
	})))

	mux.Handle("POST /users/{username}/lock", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Users.Lock(r.Context(), r.PathValue("username")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/users", http.StatusSeeOther)
	})))

	mux.Handle("POST /users/{username}/unlock", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Users.Unlock(r.Context(), r.PathValue("username")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/users", http.StatusSeeOther)
	})))

	mux.Handle("POST /users/{username}/password", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		if err := cfg.Users.SetPassword(r.Context(), r.PathValue("username"), r.FormValue("password")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/users", http.StatusSeeOther)
	})))

	mux.Handle("POST /users/{username}/ssh-key", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		if err := cfg.Users.AddSSHKey(r.Context(), r.PathValue("username"), r.FormValue("public_key")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/users", http.StatusSeeOther)
	})))
}

func usersPage(users []linuxuser.User, message string) string {
	var rows strings.Builder

	for _, u := range users {
		status := "Active"
		statusClass := "ok"
		lockAction := fmt.Sprintf(`<form method="post" action="/users/%s/lock"><button class="secondary">Lock</button></form>`, html.EscapeString(u.Username))
		if u.Locked {
			status = "Locked"
			statusClass = "warn"
			lockAction = fmt.Sprintf(`<form method="post" action="/users/%s/unlock"><button class="secondary">Unlock</button></form>`, html.EscapeString(u.Username))
		}

		fmt.Fprintf(&rows, `
		<tr>
			<td><strong>%s</strong><div class="muted">%s</div></td>
			<td><span class="%s">%s</span></td>
			<td>%d</td>
			<td>
				<div class="actions">
					%s
					<details>
						<summary>Password</summary>
						<form method="post" action="/users/%s/password" class="inline-form">
							<input name="password" type="password" minlength="8" placeholder="New password" required>
							<button>Save</button>
						</form>
					</details>
					<details>
						<summary>SSH key</summary>
						<form method="post" action="/users/%s/ssh-key" class="inline-form wide">
							<textarea name="public_key" rows="3" placeholder="ssh-ed25519 AAAA..." required></textarea>
							<button>Add key</button>
						</form>
					</details>
					<form method="post" action="/users/%s/delete" onsubmit="return confirm('Delete user %s and their home directory?')">
						<button class="danger">Delete</button>
					</form>
				</div>
			</td>
		</tr>`,
			html.EscapeString(u.Username),
			html.EscapeString(u.Home),
			statusClass,
			status,
			u.SSHKeys,
			lockAction,
			html.EscapeString(u.Username),
			html.EscapeString(u.Username),
			html.EscapeString(u.Username),
			html.EscapeString(u.Username),
		)
	}

	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="4" class="empty">No Open Go Panel users yet.</td></tr>`)
	}

	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	return `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Users · Open Go Panel</title>
	<style>
		*{box-sizing:border-box}
		body{margin:0;background:#0b1020;color:#e5e7eb;font-family:system-ui,-apple-system,sans-serif}
		header{height:64px;display:flex;align-items:center;justify-content:space-between;padding:0 28px;border-bottom:1px solid #1e293b;background:#0f172a}
		a{color:inherit;text-decoration:none}
		.brand{font-weight:800}
		nav{display:flex;gap:18px;color:#94a3b8}
		nav a.active{color:#fff}
		main{max-width:1180px;margin:0 auto;padding:38px 28px 60px}
		.top{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:24px}
		h1{margin:0;font-size:30px}
		.sub{margin:7px 0 0;color:#94a3b8}
		.panel{padding:22px;border:1px solid #253047;border-radius:16px;background:#111827}
		.create{display:grid;grid-template-columns:1fr 1fr auto;gap:10px;margin-bottom:24px}
		input,textarea{width:100%;padding:10px 12px;border:1px solid #334155;border-radius:9px;background:#0f172a;color:#fff}
		button{padding:9px 13px;border:1px solid #334155;border-radius:9px;background:#4f46e5;color:#fff;cursor:pointer}
		button.secondary{background:#111827}
		button.danger{border-color:#7f1d1d;background:#450a0a;color:#fecaca}
		table{width:100%;border-collapse:collapse}
		th,td{padding:14px 10px;border-top:1px solid #1e293b;text-align:left;vertical-align:top}
		th{color:#94a3b8;font-size:12px;text-transform:uppercase;letter-spacing:.05em}
		.muted{margin-top:4px;color:#64748b;font-size:12px}
		.ok{color:#86efac}.warn{color:#fbbf24}
		.actions{display:flex;align-items:flex-start;justify-content:flex-end;gap:8px;flex-wrap:wrap}
		details{position:relative}
		summary{list-style:none;padding:9px 13px;border:1px solid #334155;border-radius:9px;background:#111827;color:#cbd5e1;cursor:pointer;font-size:13px}
		.inline-form{position:absolute;right:0;z-index:5;width:260px;margin-top:8px;padding:12px;border:1px solid #334155;border-radius:10px;background:#0f172a;box-shadow:0 20px 50px rgba(0,0,0,.4)}
		.inline-form.wide{width:440px}
		.inline-form button{margin-top:8px}
		.alert{margin-bottom:18px;padding:11px 13px;border:1px solid #7f1d1d;border-radius:10px;background:#450a0a;color:#fecaca}
		.empty{text-align:center;color:#64748b;padding:32px}
		.note{margin-top:18px;color:#64748b;font-size:13px}
		@media(max-width:760px){.create{grid-template-columns:1fr}.top{align-items:flex-start;flex-direction:column}.inline-form.wide{width:min(440px,80vw)}}
	</style>
</head>
<body>
	<header>
		<a class="brand" href="/">Open Go Panel</a>
		<nav><a href="/">Overview</a><a class="active" href="/users">Users</a></nav>
		<form method="post" action="/logout"><button class="secondary">Logout</button></form>
	</header>
	<main>
		<div class="top">
			<div><h1>Users</h1><p class="sub">Linux accounts managed by Open Go Panel.</p></div>
		</div>
		` + alert + `
		<section class="panel">
			<form class="create" method="post" action="/users">
				<input name="username" pattern="[a-z_][a-z0-9_-]{0,31}" placeholder="username" required>
				<input name="password" type="password" minlength="8" placeholder="password (min 8 chars)" required>
				<button>Create user</button>
			</form>
			<table>
				<thead><tr><th>User</th><th>Status</th><th>SSH keys</th><th></th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
			<p class="note">Users get /bin/bash, a home directory and SSH/SFTP access through the server OpenSSH configuration.</p>
		</section>
	</main>
</body>
</html>`
}
