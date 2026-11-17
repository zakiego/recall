package pipeline_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

// The golden from testdata/gen/pipeline-goldens.ts: a scripted session
// through the TS pipeline, every gateway request and every row after each
// step.
type pipelineGolden struct {
	WorkspaceID string      `json:"workspaceId"`
	Now         string      `json:"now"`
	CreatedAt   int64       `json:"createdAt"`
	Dumps       [][2]string `json:"dumps"`
	// The vectors the TS build stored, by the text it embedded (base64
	// float32 LE). Replaying them makes the run the same on every platform.
	Vectors map[string]string `json:"vectors"`
	Steps   []struct {
		Step     map[string]any `json:"step"`
		Result   any            `json:"result"`
		Requests []string       `json:"requests"`
		Rows     [][2]any       `json:"rows"`
	} `json:"steps"`
}

type gatewayReply struct {
	Text    *string           `json:"text"`
	Status  int               `json:"status"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
}

// idNorm renames random ids to prefix_N in order of first appearance, like
// the generator's norm.
type idNorm struct {
	seen   map[string]string
	counts map[string]int
}

var idPattern = regexp.MustCompile(`\b(mem|doc|chk|per)_[0-9a-z]{16}\b`)

func (n *idNorm) str(s string) string {
	s = strings.ReplaceAll(s, apiKey(), "{{KEY}}")
	return idPattern.ReplaceAllStringFunc(s, func(id string) string {
		if v, ok := n.seen[id]; ok {
			return v
		}
		prefix := id[:strings.IndexByte(id, '_')]
		n.counts[prefix]++
		v := fmt.Sprintf("%s_%d", prefix, n.counts[prefix])
		n.seen[id] = v
		return v
	})
}

// walk normalizes a decoded JSON value; objects here hold at most one id, so
// their key order doesn't matter.
func (n *idNorm) walk(v any) any {
	switch x := v.(type) {
	case string:
		return n.str(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = n.walk(e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = n.walk(e)
		}
		return out
	}
	return v
}

// decoded round-trips v through JSON so numbers and nulls compare like the
// golden's.
func decoded(t *testing.T, v any) any {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func dumpRows(t *testing.T, d *db.DB, query string) []any {
	t.Helper()
	rows, err := d.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestScriptedSessionMatchesTS(t *testing.T) {
	var g pipelineGolden
	testutil.LoadJSON(t, "pipeline.json", &g)
	t.Setenv("EXTRACT_MODEL", "claude-haiku-5-5")
	t.Setenv("FALLBACK_MODEL", "claude-sonnet-5-5")
	d := openDB(t)
	ws := g.WorkspaceID

	var mu sync.Mutex
	var queue []gatewayReply
	var requests []string
	do := func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		system, _ := testkit.ChatRequest(b)
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, string(b))
		if r.URL.String() != "http://gateway.test/v1/chat/completions" {
			return nil, fmt.Errorf("network access refused: %s", r.URL)
		}
		if strings.Contains(system, "profile") {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse(`{"static":"Static profile.","dynamic":"Dynamic profile."}`)))}, nil
		}
		reply := gatewayReply{Text: str(`{"add":[]}`)}
		if len(queue) > 0 {
			reply, queue = queue[0], queue[1:]
		}
		if reply.Text != nil {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse(*reply.Text)))}, nil
		}
		h := http.Header{"Content-Type": {"application/json"}}
		for k, v := range reply.Headers {
			h.Set(k, v)
		}
		return &http.Response{StatusCode: reply.Status, Header: h, Body: io.NopCloser(strings.NewReader(reply.Body))}, nil
	}
	r := newRunner(d, pipeline.Deps{Do: do, Now: func() time.Time { return date(g.Now) }})

	// Embed through the TS vectors; a text the TS run never stored (a
	// deduped add) falls back to the model.
	var fallbacks []string
	lookup := func(texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		var missing []string
		var at []int
		for i, text := range texts {
			b64, ok := g.Vectors[text]
			if !ok {
				missing, at = append(missing, text), append(at, i)
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				return nil, err
			}
			v := make([]float32, len(raw)/4)
			for k := range v {
				v[k] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*k:]))
			}
			out[i] = v
		}
		if len(missing) > 0 {
			fallbacks = append(fallbacks, missing...)
			vs, err := embed.Embed(missing, embed.Passage)
			if err != nil {
				return nil, err
			}
			for k, i := range at {
				out[i] = vs[k]
			}
		}
		return out, nil
	}
	origEmbed, origOne := pipeline.EmbedPassages, memories.EmbedPassage
	pipeline.EmbedPassages = lookup
	memories.EmbedPassage = func(text string) ([]float32, error) {
		v, err := lookup([]string{text})
		if err != nil {
			return nil, err
		}
		return v[0], nil
	}
	t.Cleanup(func() { pipeline.EmbedPassages, memories.EmbedPassage = origEmbed, origOne })
	defer func() { t.Logf("%d texts embedded by the model, not the TS vectors: %q", len(fallbacks), fallbacks) }()

	norm := &idNorm{seen: map[string]string{}, counts: map[string]int{}}
	last := map[string]any{}
	var added []string
	for i, gs := range g.Steps {
		step := gs.Step
		label := fmt.Sprintf("step %d (%s)", i, step["op"])
		mu.Lock()
		requests = nil
		if rs, ok := step["replies"]; ok {
			b, _ := json.Marshal(rs)
			var list []gatewayReply
			json.Unmarshal(b, &list)
			queue = append(queue, list...)
		}
		mu.Unlock()
		var result any
		switch step["op"] {
		case "add":
			in := addInput(t, step["input"])
			res, err := api.AddContent(d, ws, in, time.Now().UnixMilli())
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if _, err := d.Exec(`UPDATE documents SET created_at = ? WHERE id = ?`, g.CreatedAt, res.ID); err != nil {
				t.Fatal(err)
			}
			added = append(added, res.ID)
			result = res
		case "addFile":
			in := step["input"].(map[string]any)
			name, mime := in["name"].(string), in["mime"].(string)
			res, err := api.AddFile(d, ws, api.FileInput{Container: in["container"].(string), Name: name, Mime: mime, Bytes: []byte(in["text"].(string))}, time.Now().UnixMilli())
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if _, err := d.Exec(`UPDATE documents SET created_at = ? WHERE id = ?`, g.CreatedAt, res.ID); err != nil {
				t.Fatal(err)
			}
			added = append(added, res.ID)
			result = res
		case "uploads":
			list := []any{}
			entries, _ := os.ReadDir(memories.UploadsDir())
			for _, e := range entries {
				list = append(list, e.Name())
			}
			result = list
		case "sql":
			args, _ := step["args"].([]any)
			if _, err := d.Exec(step["sql"].(string), args...); err != nil {
				t.Fatal(err)
			}
		case "drain":
			adv, _ := step["advanceMs"].(float64)
			for n := 0; n < 50; n++ {
				j, err := r.RunOnce(time.Now().UnixMilli() + int64(adv))
				if err != nil {
					t.Fatal(err)
				}
				if j == nil {
					break
				}
			}
			if len(queue) > 0 {
				t.Fatalf("%s left %d replies", label, len(queue))
			}
		case "delete":
			ok, err := memories.DeleteDocument(d, ws, added[int(step["nth"].(float64))])
			if err != nil {
				t.Fatal(err)
			}
			result = ok
		case "forget":
			var id string
			if err := d.QueryRow(`SELECT id FROM memories WHERE fact = ? AND valid_to IS NULL`, step["fact"]).Scan(&id); err != nil {
				t.Fatal(err)
			}
			m, _, err := memories.ForgetMemory(d, ws, id, time.Now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			result = m.ValidTo != nil
		default:
			t.Fatalf("unknown op %v", step["op"])
		}

		// Normalize in the generator's order: result, requests, rows.
		var gotResult any
		if result != nil {
			gotResult = norm.walk(decoded(t, result))
		}
		if !reflect.DeepEqual(gotResult, gs.Result) {
			t.Fatalf("%s result\n got %v\nwant %v", label, gotResult, gs.Result)
		}
		mu.Lock()
		reqs := append([]string(nil), requests...)
		mu.Unlock()
		if len(reqs) != len(gs.Requests) {
			t.Fatalf("%s: %d gateway requests, want %d", label, len(reqs), len(gs.Requests))
		}
		for k := range reqs {
			if got := norm.str(reqs[k]); got != gs.Requests[k] {
				t.Fatalf("%s request %d\n got %s\nwant %s", label, k, got, gs.Requests[k])
			}
		}
		for k, dump := range g.Dumps {
			name, query := dump[0], dump[1]
			rows := decoded(t, dumpRows(t, d, query))
			same := reflect.DeepEqual(rows, last[name])
			last[name] = rows
			want := gs.Rows[k][1]
			if want == nil {
				if !same {
					t.Fatalf("%s %s changed; the TS rows did not:\n got %v", label, name, norm.walk(rows))
				}
				continue
			}
			got := norm.walk(rows)
			if !reflect.DeepEqual(got, want) {
				gr, _ := json.Marshal(got)
				wr, _ := json.Marshal(want)
				t.Fatalf("%s %s\n got %s\nwant %s", label, name, gr, wr)
			}
		}
	}
}

// addInput reads a step's addContent input (zod's addInput comes with M5).
func addInput(t *testing.T, v any) api.AddInput {
	t.Helper()
	b, _ := json.Marshal(v)
	b = []byte(strings.ReplaceAll(string(b), "{{KEY}}", apiKey()))
	var raw struct {
		Container    string            `json:"container"`
		Content      *string           `json:"content"`
		Messages     []api.ChatMessage `json:"messages"`
		Title        *string           `json:"title"`
		Metadata     json.RawMessage   `json:"metadata"`
		SourceID     *string           `json:"source_id"`
		DocumentDate *string           `json:"document_date"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	in := api.AddInput{Container: raw.Container, Content: raw.Content, Messages: raw.Messages, Title: raw.Title, SourceID: raw.SourceID, DocumentDate: raw.DocumentDate}
	if raw.Metadata != nil {
		o, err := jsonx.ParseObject(raw.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		in.Metadata = o
	}
	return in
}
