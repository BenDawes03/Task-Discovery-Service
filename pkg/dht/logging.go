package dht

import "io"

// SetLogOutput redirects DHT logging output (registry + network).
func SetLogOutput(w io.Writer) {
	if w == nil {
		return
	}
	registryLogger.SetOutput(w)
	netLogger.SetOutput(w)
}

// SetQuiet silences DHT logging.
func SetQuiet() {
	registryLogger.SetOutput(io.Discard)
	netLogger.SetOutput(io.Discard)
}
