package embed

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type goldenCase struct {
	Kind   string  `json:"kind"`
	Text   string  `json:"text"`
	Fed    string  `json:"fed"`
	IDs    []int64 `json:"ids"`
	Vector string  `json:"vector"`
}

type goldenFile struct {
	Model    string       `json:"model"`
	Dims     int          `json:"dims"`
	MaxBatch int          `json:"maxBatch"`
	Texts    []string     `json:"texts"`
	Cases    []goldenCase `json:"cases"`
}

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata")
}

// testModelsDir is apps/app/data/models unless MODELS_DIR says otherwise.
func testModelsDir() string {
	if d := os.Getenv("MODELS_DIR"); d != "" {
		return d
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "app", "data", "models")
}

func loadGolden(t *testing.T) *goldenFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testdataDir(), "embed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Cases) < 400 {
		t.Fatalf("golden has %d cases, want at least 400", len(g.Cases))
	}
	return &g
}

var (
	tokOnce sync.Once
	tok     *Tokenizer
	tokErr  error
)

func testTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	tokOnce.Do(func() {
		tok, tokErr = LoadTokenizer(filepath.Join(testModelsDir(), ModelID))
	})
	if tokErr != nil {
		t.Fatalf("load tokenizer: %v", tokErr)
	}
	return tok
}

func decodeVector(t *testing.T, s string) []float32 {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func TestFedTextMatchesGolden(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.Cases {
		if got := FedText(c.Text, Kind(c.Kind)); got != c.Fed {
			t.Errorf("case %d: fed text %q, want %q", i, got, c.Fed)
		}
	}
}

func TestTokenIDsMatchGolden(t *testing.T) {
	g := loadGolden(t)
	tk := testTokenizer(t)
	bad := 0
	for i, c := range g.Cases {
		got := tk.Encode(c.Fed)
		if !equalIDs(got, c.IDs) {
			bad++
			if bad <= 10 {
				t.Errorf("case %d (%s, %q): ids differ\n got  %v\n want %v", i, c.Kind, c.Fed, head(got), head(c.IDs))
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d cases have different token ids", bad, len(g.Cases))
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func head(ids []int64) []int64 {
	if len(ids) > 40 {
		return ids[:40]
	}
	return ids
}

// testLibPath is the ONNX Runtime library from scripts/fetch-onnxruntime.sh
// unless ONNXRUNTIME_LIB says otherwise.
func testLibPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ONNXRUNTIME_LIB"); p != "" {
		return p
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	p, err := DefaultLibPath(root)
	if err != nil {
		t.Fatalf("%v (run scripts/fetch-onnxruntime.sh)", err)
	}
	return p
}

var (
	modelOnce sync.Once
	model     *Model
	modelErr  error
)

func testModel(t *testing.T) *Model {
	t.Helper()
	modelOnce.Do(func() { model, modelErr = LoadModel(testModelsDir(), testLibPath(t)) })
	if modelErr != nil {
		t.Fatalf("load model: %v", modelErr)
	}
	return model
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// platformVectors returns the TS build's vectors for the golden cases on this
// platform and whether they must match bit for bit. ONNX Runtime picks
// different kernels per platform, so the TS build's own vectors differ between
// macOS and Linux. On linux/amd64 the kernels also depend on the CPU's
// instruction sets, so there the vectors only have to reach cosine 0.9999.
func platformVectors(t *testing.T, g *goldenFile) ([]string, bool) {
	t.Helper()
	plat := runtime.GOOS + "/" + runtime.GOARCH
	vecs := make([]string, len(g.Cases))
	switch plat {
	case "darwin/arm64":
		for i, c := range g.Cases {
			vecs[i] = c.Vector
		}
		return vecs, true
	case "linux/arm64", "linux/amd64":
		raw, err := os.ReadFile(filepath.Join(testdataDir(), "embed-linux-"+runtime.GOARCH+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var p goldenFile
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Cases) != len(g.Cases) {
			t.Fatalf("%s: %d vectors for %d cases", plat, len(p.Cases), len(g.Cases))
		}
		for i, c := range p.Cases {
			if c.Kind != g.Cases[i].Kind || c.Text != g.Cases[i].Text {
				t.Fatalf("%s: case %d is not the golden's case %d", plat, i, i)
			}
			vecs[i] = c.Vector
		}
		return vecs, runtime.GOARCH == "arm64"
	}
	t.Fatalf("no TS vectors for %s (testdata/gen/embed-linux.sh makes them for Linux)", plat)
	return nil, false
}

func TestVectorsMatchGolden(t *testing.T) {
	g := loadGolden(t)
	m := testModel(t)
	want, strict := platformVectors(t, g)
	idx := map[string][]int{}
	for i, c := range g.Cases {
		idx[c.Kind] = append(idx[c.Kind], i)
	}
	worst, exact, total := 1.0, 0, 0
	for kind, cases := range idx {
		texts := make([]string, len(cases))
		for j, i := range cases {
			texts[j] = g.Cases[i].Text
		}
		vecs, err := m.Embed(texts, Kind(kind))
		if err != nil {
			t.Fatal(err)
		}
		for j, i := range cases {
			w := decodeVector(t, want[i])
			if len(vecs[j]) != Dims || len(w) != Dims {
				t.Fatalf("%s %d: dims %d and %d", kind, i, len(vecs[j]), len(w))
			}
			cs := cosine(vecs[j], w)
			worst = math.Min(worst, cs)
			same := true
			for k := range w {
				if vecs[j][k] != w[k] {
					same = false
					break
				}
			}
			if same {
				exact++
			} else if strict {
				t.Errorf("%s %q: not bit-identical (cosine %.9f)", kind, g.Cases[i].Text, cs)
			}
			total++
			if cs < 0.9999 {
				t.Errorf("%s %q: cosine %.6f", kind, g.Cases[i].Text, cs)
			}
		}
	}
	t.Logf("%s: %d vectors, %d bit-identical, worst cosine %.9f", runtime.GOOS+"/"+runtime.GOARCH, total, exact, worst)
}

// Batch-mates change a vector: the quantized model quantizes activations over
// the whole padded batch (cosine about 0.999 between a text embedded alone and
// in a batch). So Embed must group exactly like embed-model.ts: consecutive
// groups of MaxBatch in call order, and callers must pass the same lists the
// TS callers pass.
func TestEmbedGroupsByFourInOrder(t *testing.T) {
	m := testModel(t)
	texts := []string{"Prefers Adidas running shoes", strings.Repeat("long text ", 200), "x", "y", "Is allergic to peanuts", "z"}
	all, err := m.Embed(texts, Passage)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Embed(texts[:4], Passage)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := m.Embed(texts[4:], Passage)
	if err != nil {
		t.Fatal(err)
	}
	groups := append(first, rest...)
	if len(all) != len(texts) || len(groups) != len(texts) {
		t.Fatalf("%d and %d vectors for %d texts", len(all), len(groups), len(texts))
	}
	for i := range all {
		for k := range all[i] {
			if all[i][k] != groups[i][k] {
				t.Fatalf("text %d: Embed of 6 differs from Embed of 4 then 2", i)
			}
		}
	}
	alone, err := m.Embed(texts[:1], Passage)
	if err != nil {
		t.Fatal(err)
	}
	if cs := cosine(alone[0], all[0]); cs == 1 || cs < 0.99 {
		t.Fatalf("cosine %.9f between alone and batched: expected a small difference", cs)
	}
	empty, err := m.Embed(nil, Query)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input: %v %v", empty, err)
	}
}
