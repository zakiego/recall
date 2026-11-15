package cli

import (
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// Prompt is readPassword's default prompt.
const Prompt = "Password (12+ chars): "

// ReadPassword is readPassword (password-prompt.ts). Piped stdin is read
// whole, so `echo pw | recall create-user x` keeps working. On a terminal the
// prompt goes to out (stderr, so stdout carries only the result) and what is
// typed is never echoed.
func ReadPassword(in *os.File, out io.Writer) (string, error) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return readPiped(in)
	}
	io.WriteString(out, Prompt)
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	pw, err := readTerminal(in)
	term.Restore(fd, state)
	io.WriteString(out, "\n")
	return pw, err
}

func readPiped(in io.Reader) (string, error) {
	b, err := io.ReadAll(in)
	if err != nil {
		return "", err
	}
	return strx.Trim(string(b)), nil
}

// errEOF is the input ending before Enter. Node's readline keeps waiting
// then (a terminal in raw mode never ends on its own).
var errEOF = errors.New("input ended before Enter")

// readTerminal reads one line the way Node 24's readline does in terminal
// mode (internal/readline/utils.js emitKeys and interface.js kTtyWrite):
// cursor movement, word and line deletion, the kill ring and yank, undo and
// redo. Ctrl+C cancels, Ctrl+D on an empty line aborts.
func readTerminal(in io.Reader) (string, error) {
	r := newKeyReader(in)
	e := &lineEditor{}
	for {
		k, err := readKey(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errEOF
			}
			return "", err
		}
		done, err := e.key(k)
		if err != nil {
			return "", err
		}
		if done {
			return strx.Trim(string(e.line)), nil
		}
	}
}

// key is a keypress as emitKeys reports it. s is "" for escape sequences
// (emitKeys passes undefined), else the character.
type key struct {
	name              string
	ctrl, meta, shift bool
	s                 string
	seq               string
}

// readKey is one step of emitKeys.
func readKey(r *keyReader) (key, error) {
	ch, err := readChar(r)
	if err != nil {
		return key{}, err
	}
	seq := ch
	escaped := false
	k := key{}
	if ch == "\x1b" {
		escaped = true
		if ch, err = readChar(r); err != nil {
			return key{}, err
		}
		seq += ch
		if ch == "\x1b" {
			if ch, err = readChar(r); err != nil {
				return key{}, err
			}
			seq += ch
		}
	}
	switch {
	case escaped && (ch == "O" || ch == "["):
		code, modifier := ch, 0
		next := func() error {
			c, err := readChar(r)
			ch = c
			seq += c
			return err
		}
		digit := func() bool { return len(ch) == 1 && ch[0] >= '0' && ch[0] <= '9' }
		if ch == "O" {
			if err := next(); err != nil {
				return key{}, err
			}
			if digit() {
				modifier = int(ch[0]-'0') - 1
				if err := next(); err != nil {
					return key{}, err
				}
			}
			code += ch
		} else {
			if err := next(); err != nil {
				return key{}, err
			}
			if ch == "[" {
				code += ch
				if err := next(); err != nil {
					return key{}, err
				}
			}
			cmdStart := len(seq) - len(ch)
			if digit() {
				if err := next(); err != nil {
					return key{}, err
				}
				if digit() {
					if err := next(); err != nil {
						return key{}, err
					}
					if digit() {
						if err := next(); err != nil {
							return key{}, err
						}
					}
				}
			}
			if ch == ";" {
				if err := next(); err != nil {
					return key{}, err
				}
				if digit() {
					if err := next(); err != nil {
						return key{}, err
					}
				}
			}
			cmd := seq[cmdStart:]
			if m := tildeCmd.FindStringSubmatch(cmd); m != nil {
				if m[4] != "" {
					code += m[4]
				} else {
					code += m[1] + m[3]
					modifier = 0
					if m[2] != "" {
						modifier = int(m[2][0]-'0') - 1
					}
				}
			} else if m := letterCmd.FindStringSubmatch(cmd); m != nil {
				code += m[4]
				modifier = 0
				if m[3] != "" {
					modifier = int(m[3][0]-'0') - 1
				}
			} else {
				code += cmd
			}
		}
		k.ctrl = modifier&4 != 0
		k.meta = modifier&10 != 0
		k.shift = modifier&1 != 0
		k.name = escNames[code]
		switch code {
		case "[a", "[b", "[c", "[d", "[e", "[2$", "[3$", "[5$", "[6$", "[7$", "[8$", "[Z":
			k.shift = true
		case "Oa", "Ob", "Oc", "Od", "Oe", "[2^", "[3^", "[5^", "[6^", "[7^", "[8^":
			k.ctrl = true
		}
		if k.name == "" {
			k.name = "undefined"
		}
	case ch == "\r":
		k.name, k.meta = "return", escaped
	case ch == "\n":
		k.name, k.meta = "enter", escaped
	case ch == "\t":
		k.name, k.meta = "tab", escaped
	case ch == "\b" || ch == "\x7f":
		k.name, k.meta = "backspace", escaped
	case ch == "\x1b":
		k.name, k.meta = "escape", escaped
	case ch == " ":
		k.name, k.meta = "space", escaped
	case !escaped && len(ch) == 1 && ch[0] <= 0x1a:
		k.name, k.ctrl = string(rune(ch[0]+'a'-1)), true
	case len(ch) == 1 && (ch[0] >= '0' && ch[0] <= '9' || ch[0] >= 'A' && ch[0] <= 'Z' || ch[0] >= 'a' && ch[0] <= 'z'):
		k.name = strings.ToLower(ch)
		k.shift = ch[0] >= 'A' && ch[0] <= 'Z'
		k.meta = escaped
	case escaped:
		// An unnamed character after ESC: meta, nothing to insert. Nothing
		// at all (the timeout) is the Escape key.
		k.meta = true
		if ch == "" {
			k.name = "escape"
		}
	}
	k.seq = seq
	if !escaped {
		k.s = seq
	}
	return k, nil
}

// escapeTimeout is readline's escapeCodeTimeout: an ESC that ends a chunk
// of input and gets nothing within it is the Escape key.
const escapeTimeout = 500 * time.Millisecond

// keyReader is emitKeypressEvents' feed: input chunks decoded as UTF-8
// (a bad byte is U+FFFD, a split sequence waits for the next chunk), one
// code point at a time.
type keyReader struct {
	chunks  chan []byte
	err     error
	pending []byte
	queue   []rune
	escTail bool
}

func newKeyReader(in io.Reader) *keyReader {
	k := &keyReader{chunks: make(chan []byte)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				k.chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				k.err = err
				close(k.chunks)
				return
			}
		}
	}()
	return k
}

func (k *keyReader) decode(b []byte) {
	b = append(k.pending, b...)
	k.pending = nil
	for len(b) > 0 {
		if !utf8.FullRune(b) {
			k.pending = b
			return
		}
		r, size := utf8.DecodeRune(b)
		k.queue = append(k.queue, r)
		b = b[size:]
	}
}

// readChar is the next code point; "" when an ESC ended the last chunk and
// nothing followed within escapeTimeout.
func readChar(k *keyReader) (string, error) {
	for len(k.queue) == 0 {
		var timeout <-chan time.Time
		if k.escTail {
			timeout = time.After(escapeTimeout)
		}
		select {
		case b, ok := <-k.chunks:
			if !ok {
				if k.err == nil || errors.Is(k.err, io.EOF) {
					return "", io.EOF
				}
				return "", k.err
			}
			k.escTail = false
			k.decode(b)
		case <-timeout:
			k.escTail = false
			return "", nil
		}
	}
	r := k.queue[0]
	k.queue = k.queue[1:]
	k.escTail = len(k.queue) == 0 && r == 0x1b
	return string(r), nil
}

func mustRe(s string) *regexp.Regexp { return regexp.MustCompile(s) }

var (
	tildeCmd  = mustRe(`^(?:(\d\d?)(?:;(\d))?([~^$])|(\d{3}~))$`)
	letterCmd = mustRe(`^((\d;)?(\d))?([A-Za-z])$`)
	escNames  = map[string]string{
		"[P": "f1", "[Q": "f2", "[R": "f3", "[S": "f4",
		"OP": "f1", "OQ": "f2", "OR": "f3", "OS": "f4",
		"[11~": "f1", "[12~": "f2", "[13~": "f3", "[14~": "f4",
		"[200~": "paste-start", "[201~": "paste-end",
		"[[A": "f1", "[[B": "f2", "[[C": "f3", "[[D": "f4", "[[E": "f5",
		"[15~": "f5", "[17~": "f6", "[18~": "f7", "[19~": "f8", "[20~": "f9", "[21~": "f10", "[23~": "f11", "[24~": "f12",
		"[A": "up", "[B": "down", "[C": "right", "[D": "left", "[E": "clear", "[F": "end", "[H": "home",
		"OA": "up", "OB": "down", "OC": "right", "OD": "left", "OE": "clear", "OF": "end", "OH": "home",
		"[1~": "home", "[2~": "insert", "[3~": "delete", "[4~": "end", "[5~": "pageup", "[6~": "pagedown",
		"[[5~": "pageup", "[[6~": "pagedown", "[7~": "home", "[8~": "end",
		"[a": "up", "[b": "down", "[c": "right", "[d": "left", "[e": "clear",
		"[2$": "insert", "[3$": "delete", "[5$": "pageup", "[6$": "pagedown", "[7$": "home", "[8$": "end",
		"Oa": "up", "Ob": "down", "Oc": "right", "Od": "left", "Oe": "clear",
		"[2^": "insert", "[3^": "delete", "[5^": "pageup", "[6^": "pagedown", "[7^": "home", "[8^": "end",
		"[Z": "tab",
	}
)

type editState struct {
	line   []rune
	cursor int
}

// lineEditor is readline's line state in terminal mode. Positions are code
// points; readline moves by code point too (charLengthAt/charLengthLeft).
type lineEditor struct {
	line       []rune
	cursor     int
	killRing   [][]rune
	killCursor int
	yanking    bool
	undo, redo []editState
}

const (
	maxKillRing = 32
	maxUndo     = 2048
)

// key is kTtyWrite for one keypress; done when the line is complete.
func (e *lineEditor) key(k key) (bool, error) {
	if !k.meta || k.name != "y" {
		e.yanking = false
	}
	if k.seq != "" {
		switch []rune(k.seq)[0] {
		case 0x1f:
			e.doUndo()
			return false, nil
		case 0x1e:
			e.doRedo()
			return false, nil
		}
	}
	if k.name == "escape" {
		return false, nil
	}
	switch {
	case k.ctrl && k.shift:
		switch k.name {
		case "backspace":
			e.deleteLineLeft()
		case "delete":
			e.deleteLineRight()
		}
	case k.ctrl:
		switch k.name {
		case "c":
			return false, errors.New("cancelled")
		case "h":
			e.deleteLeft()
		case "d":
			if e.cursor == 0 && len(e.line) == 0 {
				return false, errors.New("Aborted with Ctrl+D")
			} else if e.cursor < len(e.line) {
				e.deleteRight()
			}
		case "u":
			e.deleteLineLeft()
		case "k":
			e.deleteLineRight()
		case "a":
			e.cursor = 0
		case "e":
			e.cursor = len(e.line)
		case "b":
			e.move(-1)
		case "f":
			e.move(1)
		case "y":
			e.yank()
		case "w", "backspace":
			e.deleteWordLeft()
		case "delete":
			e.deleteWordRight()
		case "left":
			e.wordLeft()
		case "right":
			e.wordRight()
		}
	case k.meta:
		switch k.name {
		case "b":
			e.wordLeft()
		case "f":
			e.wordRight()
		case "d", "delete":
			e.deleteWordRight()
		case "backspace":
			e.deleteWordLeft()
		case "y":
			e.yankPop()
		}
	default:
		switch k.name {
		case "return", "enter":
			return true, nil
		case "backspace":
			e.deleteLeft()
		case "delete":
			e.deleteRight()
		case "left":
			e.move(-1)
		case "right":
			e.move(1)
		case "home":
			e.cursor = 0
		case "end":
			e.cursor = len(e.line)
		case "up", "down":
			// No history and one line: nothing moves.
		default:
			if k.s == "" {
				return false, nil
			}
			// U+2028 and U+2029 end the line like Enter.
			if k.s == " " || k.s == " " {
				return true, nil
			}
			e.insert([]rune(k.s))
		}
	}
	return false, nil
}

func (e *lineEditor) beforeEdit() {
	e.undo = append(e.undo, editState{append([]rune(nil), e.line...), e.cursor})
	if len(e.undo) > maxUndo {
		e.undo = e.undo[1:]
	}
}

func (e *lineEditor) set(line []rune, cursor int) {
	e.line, e.cursor = line, cursor
}

func (e *lineEditor) move(dx int) {
	e.cursor = max(0, min(len(e.line), e.cursor+dx))
}

func (e *lineEditor) insert(c []rune) {
	e.beforeEdit()
	line := append(append(append([]rune{}, e.line[:e.cursor]...), c...), e.line[e.cursor:]...)
	e.set(line, e.cursor+len(c))
}

func (e *lineEditor) deleteLeft() {
	if e.cursor > 0 && len(e.line) > 0 {
		e.beforeEdit()
		e.set(append(append([]rune{}, e.line[:e.cursor-1]...), e.line[e.cursor:]...), e.cursor-1)
	}
}

func (e *lineEditor) deleteRight() {
	if e.cursor < len(e.line) {
		e.beforeEdit()
		e.set(append(append([]rune{}, e.line[:e.cursor]...), e.line[e.cursor+1:]...), e.cursor)
	}
}

// isSpace is JS \s; isWord is \w.
func isSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func isWord(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

// wordLeftLen is /^\s*(?:[^\w\s]+|\w+)?/ on the reversed text before the cursor.
func (e *lineEditor) wordLeftLen() int {
	i := e.cursor
	for i > 0 && isSpace(e.line[i-1]) {
		i--
	}
	switch {
	case i > 0 && isWord(e.line[i-1]):
		for i > 0 && isWord(e.line[i-1]) {
			i--
		}
	case i > 0:
		for i > 0 && !isWord(e.line[i-1]) && !isSpace(e.line[i-1]) {
			i--
		}
	}
	return e.cursor - i
}

// wordRightLen is /^(?:\s+|[^\w\s]+|\w+)\s*/ after the cursor, or with
// nonWord /^(?:\s+|\W+|\w+)\s*/.
func (e *lineEditor) wordRightLen(nonWord bool) int {
	i := e.cursor
	n := len(e.line)
	switch {
	case isSpace(e.line[i]):
		for i < n && isSpace(e.line[i]) {
			i++
		}
	case isWord(e.line[i]):
		for i < n && isWord(e.line[i]) {
			i++
		}
	case nonWord:
		for i < n && !isWord(e.line[i]) {
			i++
		}
	default:
		for i < n && !isWord(e.line[i]) && !isSpace(e.line[i]) {
			i++
		}
	}
	for i < n && isSpace(e.line[i]) {
		i++
	}
	return i - e.cursor
}

func (e *lineEditor) wordLeft() {
	if e.cursor > 0 {
		e.move(-e.wordLeftLen())
	}
}

func (e *lineEditor) wordRight() {
	if e.cursor < len(e.line) {
		e.move(e.wordRightLen(false))
	}
}

func (e *lineEditor) deleteWordLeft() {
	if e.cursor > 0 {
		e.beforeEdit()
		start := e.cursor - e.wordLeftLen()
		e.set(append(append([]rune{}, e.line[:start]...), e.line[e.cursor:]...), start)
	}
}

func (e *lineEditor) deleteWordRight() {
	if e.cursor < len(e.line) {
		e.beforeEdit()
		end := e.cursor + e.wordRightLen(true)
		e.set(append(append([]rune{}, e.line[:e.cursor]...), e.line[end:]...), e.cursor)
	}
}

func (e *lineEditor) deleteLineLeft() {
	e.beforeEdit()
	del := append([]rune{}, e.line[:e.cursor]...)
	e.set(append([]rune{}, e.line[e.cursor:]...), 0)
	e.pushKill(del)
}

func (e *lineEditor) deleteLineRight() {
	e.beforeEdit()
	del := append([]rune{}, e.line[e.cursor:]...)
	e.set(append([]rune{}, e.line[:e.cursor]...), e.cursor)
	e.pushKill(del)
}

func (e *lineEditor) pushKill(del []rune) {
	if len(del) == 0 || len(e.killRing) > 0 && string(del) == string(e.killRing[0]) {
		return
	}
	e.killRing = append([][]rune{del}, e.killRing...)
	e.killCursor = 0
	if len(e.killRing) > maxKillRing {
		e.killRing = e.killRing[:maxKillRing]
	}
}

func (e *lineEditor) yank() {
	if len(e.killRing) > 0 {
		e.yanking = true
		e.insert(e.killRing[e.killCursor])
	}
}

func (e *lineEditor) yankPop() {
	if !e.yanking || len(e.killRing) <= 1 {
		return
	}
	last := e.killRing[e.killCursor]
	e.killCursor = (e.killCursor + 1) % len(e.killRing)
	cur := e.killRing[e.killCursor]
	head := e.line[:max(0, e.cursor-len(last))]
	line := append(append(append([]rune{}, head...), cur...), e.line[e.cursor:]...)
	e.set(line, len(head)+len(cur))
}

func (e *lineEditor) doUndo() {
	if len(e.undo) == 0 {
		return
	}
	e.redo = append(e.redo, editState{e.line, e.cursor})
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.set(s.line, s.cursor)
}

func (e *lineEditor) doRedo() {
	if len(e.redo) == 0 {
		return
	}
	e.undo = append(e.undo, editState{e.line, e.cursor})
	s := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.set(s.line, s.cursor)
}
