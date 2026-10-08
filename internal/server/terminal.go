package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	panelterminal "github.com/bau59/open-go-panel/internal/terminal"
)

type terminalPageData struct {
	Users        []linuxuser.User
	Apps         []panelapp.App
	SelectedUser string
	SelectedApp  int64
	WorkDir      string
	Message      string
}

type terminalClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
}

func registerTerminalRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /terminal", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !terminalRequestIsLocal(r) {
			writeHTML(w, cfg.Logger, http.StatusForbidden, terminalUnavailablePage())
			return
		}
		users, err := cfg.Users.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		apps, err := cfg.Apps.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		username, workdir, appID, err := resolveTerminalTarget(r, users, apps)
		message := ""
		if err != nil {
			message = err.Error()
			username = "root"
			workdir = "/root"
			appID = 0
		}
		writeHTML(w, cfg.Logger, http.StatusOK, terminalPage(terminalPageData{
			Users: users, Apps: apps, SelectedUser: username, SelectedApp: appID, WorkDir: workdir, Message: message,
		}))
	})))

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				return false
			}
			u, err := url.Parse(origin)
			return err == nil && strings.EqualFold(u.Host, r.Host)
		},
	}

	mux.Handle("GET /terminal/ws", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !terminalRequestIsLocal(r) {
			http.Error(w, "terminal is available only through a local connection or SSH tunnel", http.StatusForbidden)
			return
		}
		users, err := cfg.Users.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		apps, err := cfg.Apps.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		username, workdir, _, err := resolveTerminalTarget(r, users, apps)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(64 * 1024)

		session, err := panelterminal.Start(r.Context(), username, workdir, 24, 100)
		if err != nil {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()), timeNowPlusSecond())
			return
		}
		defer session.Close()

		if cfg.State != nil {
			cfg.State.Audit(r.Context(), "terminal.open", username, "workdir="+workdir+" remote="+r.RemoteAddr)
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 32*1024)
			for {
				n, err := session.Read(buf)
				if n > 0 {
					if writeErr := conn.WriteMessage(websocket.BinaryMessage, append([]byte(nil), buf[:n]...)); writeErr != nil {
						return
					}
				}
				if err != nil {
					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "terminal closed"), timeNowPlusSecond())
					_ = conn.Close()
					return
				}
			}
		}()

		for {
			select {
			case <-done:
				return
			default:
			}
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message terminalClientMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				continue
			}
			switch message.Type {
			case "input":
				if message.Data != "" {
					if _, err := session.Write([]byte(message.Data)); err != nil {
						return
					}
				}
			case "resize":
				rows := message.Rows
				cols := message.Cols
				if rows > 500 {
					rows = 500
				}
				if cols > 500 {
					cols = 500
				}
				_ = session.Resize(rows, cols)
			}
		}
	})))
}

func terminalRequestIsLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func terminalUnavailablePage() string {
	return pageHead("Terminal") + `<body>` + appHeader("terminal") + `
	<main class="shell">
		<div class="page-head">
			<div><p class="eyebrow">Server shell</p><h1>Terminal</h1><p class="sub">The web terminal is intentionally disabled over a public panel connection.</p></div>
		</div>
		<section class="panel panel-pad">
			<h2>Connect through an SSH tunnel</h2>
			<p class="sub">This protects the root-capable terminal from being exposed directly to the Internet.</p>
			<pre class="security-output" style="margin-top:16px">ssh -L 8443:127.0.0.1:8443 root@SERVER_IP

Then open:
http://127.0.0.1:8443/terminal</pre>
		</section>
	</main>
</body></html>`
}

func timeNowPlusSecond() time.Time {
	return time.Now().Add(time.Second)
}

func resolveTerminalTarget(r *http.Request, users []linuxuser.User, apps []panelapp.App) (string, string, int64, error) {
	username := strings.TrimSpace(r.URL.Query().Get("user"))
	appID, _ := strconv.ParseInt(r.URL.Query().Get("app"), 10, 64)

	var selectedApp *panelapp.App
	if appID > 0 {
		for i := range apps {
			if apps[i].ID == appID {
				selectedApp = &apps[i]
				break
			}
		}
		if selectedApp == nil {
			return "", "", 0, fmt.Errorf("app %d not found", appID)
		}
		if username == "" {
			username = selectedApp.User
		}
		if username != selectedApp.User {
			return "", "", 0, errors.New("an app terminal must run as the app owner")
		}
	}

	if username == "" {
		username = "root"
	}
	if username == "root" {
		if selectedApp != nil {
			return "", "", 0, errors.New("root app terminals are not exposed; choose the app owner")
		}
		return "root", "/root", 0, nil
	}

	for _, managed := range users {
		if managed.Username != username {
			continue
		}
		if selectedApp != nil {
			return username, selectedApp.Root, selectedApp.ID, nil
		}
		return username, managed.Home, 0, nil
	}
	return "", "", 0, fmt.Errorf("user %q is not managed by Open Go Panel", username)
}

func terminalPage(data terminalPageData) string {
	alert := ""
	if data.Message != "" {
		alert = `<div class="alert">` + html.EscapeString(data.Message) + `</div>`
	}

	var userOptions strings.Builder
	userOptions.WriteString(`<option value="root"` + selected(data.SelectedUser, "root") + `>root</option>`)
	for _, item := range data.Users {
		fmt.Fprintf(&userOptions, `<option value="%s"%s>%s</option>`,
			html.EscapeString(item.Username),
			selected(data.SelectedUser, item.Username),
			html.EscapeString(item.Username),
		)
	}

	var appOptions strings.Builder
	appOptions.WriteString(`<option value="">Home directory</option>`)
	if data.SelectedUser != "root" {
		for _, item := range data.Apps {
			if item.User != data.SelectedUser {
				continue
			}
			selectedAttr := ""
			if item.ID == data.SelectedApp {
				selectedAttr = " selected"
			}
			fmt.Fprintf(&appOptions, `<option value="%d"%s>%s · %s</option>`,
				item.ID,
				selectedAttr,
				html.EscapeString(item.Name),
				html.EscapeString(item.Root),
			)
		}
	}

	head := strings.Replace(pageHead("Terminal"), "</head>", `
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm@5.3.0/css/xterm.css">
</head>`, 1)

	rootWarning := ""
	if data.SelectedUser == "root" {
		rootWarning = `<div class="terminal-warning">Root terminal has unrestricted server access. Commands run immediately as root.</div>`
	}

	return head + `<body>` + appHeader("terminal") + `
	<main class="shell terminal-shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Server shell</p>
				<h1>Terminal</h1>
				<p class="sub">Open an interactive shell as root or any Open Go Panel managed Linux user.</p>
			</div>
		</div>
		` + alert + `

		<section class="panel terminal-panel">
			<div class="terminal-toolbar">
				<form method="get" action="/terminal" id="terminal-target-form">
					<div>
						<label>User</label>
						<select name="user" id="terminal-user" onchange="this.form.submit()">` + userOptions.String() + `</select>
					</div>
					<div>
						<label>Working directory</label>
						<select name="app" id="terminal-app" onchange="this.form.submit()"` + func() string { if data.SelectedUser == "root" { return " disabled" }; return "" }() + `>` + appOptions.String() + `</select>
					</div>
					<div class="terminal-connect"><button class="secondary">Switch terminal</button></div>
				</form>
				<div class="terminal-target-meta">
					<span class="status-badge ok">interactive</span>
					<code>` + html.EscapeString(data.SelectedUser) + ` · ` + html.EscapeString(data.WorkDir) + `</code>
				</div>
			</div>
			` + rootWarning + `
			<div id="terminal-screen" class="terminal-screen"><div class="terminal-loading">Loading terminal…</div></div>
		</section>
	</main>

	<script src="https://cdn.jsdelivr.net/npm/xterm@5.3.0/lib/xterm.js"></script>
	<script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.js"></script>
	<script>
	(() => {
		const screen = document.getElementById('terminal-screen');
		if (typeof Terminal === 'undefined' || typeof FitAddon === 'undefined') {
			screen.innerHTML = '<div class="terminal-loading">xterm.js could not be loaded. Check browser network access.</div>';
			return;
		}

		const term = new Terminal({
			cursorBlink: true,
			convertEol: false,
			fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", monospace',
			fontSize: 13,
			scrollback: 5000,
			theme: {
				background: '#080c12',
				foreground: '#d7deea',
				cursor: '#9a90ff',
				selectionBackground: '#2b3150'
			}
		});
		const fitAddon = new FitAddon.FitAddon();
		term.loadAddon(fitAddon);
		screen.innerHTML = '';
		term.open(screen);
		fitAddon.fit();
		term.focus();

		const params = new URLSearchParams();
		params.set('user', ` + strconv.Quote(data.SelectedUser) + `);
		` + func() string {
			if data.SelectedApp <= 0 {
				return ""
			}
			return `params.set('app', '` + strconv.FormatInt(data.SelectedApp, 10) + `');`
		}() + `
		const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
		const socket = new WebSocket(scheme + '//' + location.host + '/terminal/ws?' + params.toString());
		socket.binaryType = 'arraybuffer';
		const decoder = new TextDecoder();

		const sendResize = () => {
			if (socket.readyState !== WebSocket.OPEN) return;
			socket.send(JSON.stringify({type:'resize', rows:term.rows, cols:term.cols}));
		};

		socket.addEventListener('open', () => {
			sendResize();
			term.focus();
		});
		socket.addEventListener('message', (event) => {
			if (event.data instanceof ArrayBuffer) {
				term.write(decoder.decode(new Uint8Array(event.data), {stream:true}));
			} else {
				term.write(event.data);
			}
		});
		socket.addEventListener('close', () => {
			term.write('\r\n\x1b[33m[terminal disconnected]\x1b[0m\r\n');
		});
		socket.addEventListener('error', () => {
			term.write('\r\n\x1b[31m[terminal connection error]\x1b[0m\r\n');
		});
		term.onData((data) => {
			if (socket.readyState === WebSocket.OPEN) {
				socket.send(JSON.stringify({type:'input', data}));
			}
		});

		let resizeTimer;
		window.addEventListener('resize', () => {
			clearTimeout(resizeTimer);
			resizeTimer = setTimeout(() => {
				fitAddon.fit();
				sendResize();
			}, 80);
		});
	})();
	</script>
</body></html>`
}
