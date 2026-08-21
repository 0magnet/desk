// Package hostproto is the wire between a desk pane and the host agent.
//
// It has no build tag and no dependency outside encoding/json, because both
// ends of the wire have to agree on it and the two ends compile for different
// platforms — the agent is native, the pane is js/wasm. A protocol defined
// twice is a protocol that can differ, and a round-trip test in each half is no
// guard at all: it passes perfectly against a framing the other half rejects.
// So it is defined once, here, and both halves import it.
//
// # The shape
//
// Server to client is RAW BINARY: whatever the pty produced, unframed. A
// terminal's output is already a self-describing byte stream — that is what
// escape sequences are — so wrapping it would mean parsing a wrapper to hand
// the contents to a parser. Chunk boundaries carry no meaning and need not be
// preserved.
//
// Client to server is JSON TEXT, one message per frame. It has to be framed
// because there are two kinds of thing to say and they are not distinguishable
// as bytes: keystrokes, and "the window changed size". Sending resize down the
// same channel as input is why this does not reuse xterm-go's Attach, which
// sends keystrokes as bare text frames and has nowhere to put anything else.
//
// The asymmetry is deliberate rather than an oversight: only one direction has
// more than one kind of message in it.
package hostproto

// The client-to-server message types.
const (
	// TypeInput carries keystrokes in D.
	TypeInput = "i"

	// TypeResize carries a new grid size in C and R. The agent passes it to
	// the pty as a TIOCSWINSZ, which is what makes a full-screen program
	// redraw and what makes $COLUMNS right.
	TypeResize = "r"
)

// Msg is one client-to-server message.
//
// One struct with a type tag rather than a union: the whole thing is four
// fields, and a tagged struct decodes in one pass without a second unmarshal to
// find out what it was.
type Msg struct {
	// T is one of the Type constants above. A message with any other value
	// is ignored rather than fatal — an older agent should survive a newer
	// pane inventing a message, since the alternative is a dropped shell.
	T string `json:"t"`

	// D is the input bytes for TypeInput, as a string.
	//
	// A string and not []byte (which JSON would base64) because terminal
	// input IS text: it is what the keyboard produced, plus escape sequences
	// for the keys that are not characters. Go strings do not require valid
	// UTF-8, but encoding/json escapes what it must, so a paste of arbitrary
	// bytes survives the round trip as the replacement character rather than
	// corrupting the frame.
	D string `json:"d,omitempty"`

	// C and R are columns and rows for TypeResize.
	C int `json:"c,omitempty"`
	R int `json:"r,omitempty"`
}

// TokenParam is the query parameter the token is presented in.
//
// A query parameter and not a header because the browser's WebSocket
// constructor cannot set headers — it takes a URL and a subprotocol list and
// nothing else. The token therefore appears in the agent's logs if it logs
// URLs, which is survivable for a value that lives as long as one process and
// is never written to disk.
const TokenParam = "token"

// Path is where the agent serves the pty endpoint.
const Path = "/host/pty"
