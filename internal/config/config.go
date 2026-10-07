package config

import "os"

const defaultListenAddr = ":8443"

type Config struct {
	ListenAddr string
}

func Load() Config {
	listenAddr := os.Getenv("OGP_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	return Config{
		ListenAddr: listenAddr,
	}
}
