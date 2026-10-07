package server

import (
	"fmt"
	"log/slog"
	"net/http"
)

func New(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		_, err := fmt.Fprint(w, `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Open Go Panel</title>
	<style>
		body {
			margin: 0;
			min-height: 100vh;
			display: grid;
			place-items: center;
			background: #0b1020;
			color: #e5e7eb;
			font-family: system-ui, sans-serif;
		}
		main {
			max-width: 720px;
			padding: 32px;
		}
		h1 {
			margin: 0 0 12px;
			font-size: 40px;
		}
		p {
			margin: 0;
			color: #9ca3af;
		}
	</style>
</head>
<body>
	<main>
		<h1>Open Go Panel</h1>
		<p>Server control panel is running.</p>
	</main>
</body>
</html>`)
		if err != nil {
			logger.Error("write response failed", "err", err)
		}
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		if _, err := w.Write([]byte("ok\n")); err != nil {
			logger.Error("write health response failed", "err", err)
		}
	})

	return mux
}
