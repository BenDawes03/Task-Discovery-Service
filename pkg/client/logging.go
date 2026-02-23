package client

import "io"

// SetLogOutput redirects the client-proxy logger output.
func SetLogOutput(w io.Writer) {
	if w == nil {
		return
	}
	logger.SetOutput(w)
}

// SetQuiet silences client-proxy logging.
func SetQuiet() {
	logger.SetOutput(io.Discard)
}
