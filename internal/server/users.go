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

	for _, user := range users {
		statusClass := "ok"
		status := "Active"
		lockLabel := "Lock"
		lockPath := "lock"
		if user.Locked {
			statusClass = "warn"
			status = "Locked"
			lockLabel = "Unlock"
			lockPath = "unlock"
		}

		username := html.EscapeString(user.Username)
		fmt.Fprintf(&rows, `
		<tr>
			<td><strong>%s</strong><div class="muted">%s</div></td>
			<td><span class="status-badge %s">%s</span></td>
			<td><span class="badge">%d keys</span></td>
			<td>
				<div class="actions">
					<form method="post" action="/users/%s/%s"><button class="secondary">%s</button></form>
					<details>
						<summary class="secondary">Password</summary>
						<form method="post" action="/users/%s/password" class="inline-popover">
							<label>New password</label>
							<input name="password" type="password" minlength="8" placeholder="Minimum 8 characters" required>
							<button class="button">Save password</button>
						</form>
					</details>
					<details>
						<summary class="secondary">SSH key</summary>
						<form method="post" action="/users/%s/ssh-key" class="inline-popover wide">
							<label>Public key</label>
							<textarea name="public_key" rows="4" placeholder="ssh-ed25519 AAAA..." required></textarea>
							<button class="button">Add key</button>
						</form>
					</details>
					<form method="post" action="/users/%s/delete" onsubmit="return confirm('Delete user %s and their home directory?')">
						<button class="danger">Delete</button>
					</form>
				</div>
			</td>
		</tr>`,
			username,
			html.EscapeString(user.Home),
			statusClass,
			status,
			user.SSHKeys,
			username,
			lockPath,
			lockLabel,
			username,
			username,
			username,
			username,
		)
	}

	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="4" class="empty">No managed users yet.</td></tr>`)
	}

	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	return pageHead("Users") + `<body>` + appHeader("users") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Access</p>
				<h1>Users</h1>
				<p class="sub">Linux accounts managed by Open Go Panel.</p>
			</div>
		</div>
		` + alert + `
		<section class="panel">
			<form class="toolbar" method="post" action="/users" style="grid-template-columns:1fr 1fr auto">
				<input name="username" pattern="[a-z_][a-z0-9_-]{0,31}" placeholder="Username" required>
				<input name="password" type="password" minlength="8" placeholder="Password, minimum 8 characters" required>
				<button class="button">Create user</button>
			</form>
			<table>
				<thead><tr><th>User</th><th>Status</th><th>SSH</th><th></th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
		<p class="note" style="margin:14px 4px 0">Users receive /bin/bash, a home directory and SSH/SFTP access through OpenSSH.</p>
	</main>
</body>
</html>`
}
