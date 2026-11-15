// Command spike is the M0 check that runs inside the Linux image: it loads the
// ONNX Runtime library and the model, compares token ids and vectors with the
// TS goldens (made on macOS arm64), and opens a copy of the TS-seeded database
// with sqlite-vec and FTS5. Then it holds for a while so `docker stats` can
// read the memory with the model loaded.
//
//	spike <embed.json> <seeded.db copy> <modelsDir> <libonnxruntime> <holdSeconds> [platform vectors]
//
// The optional last file holds the TS build's vectors for the same cases made
// on this platform (testdata/gen/embed-linux.sh); vectors are compared with it
// instead of with the macOS ones in embed.json.
package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
)

type golden struct {
	Cases []struct {
		Kind   string  `json:"kind"`
		Text   string  `json:"text"`
		Fed    string  `json:"fed"`
		IDs    []int64 `json:"ids"`
		Vector string  `json:"vector"`
	} `json:"cases"`
}

func must(err error) {
	if err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
}

func main() {
	goldenPath, dbPath, modelsDir, lib := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	hold, _ := strconv.Atoi(os.Args[5])

	d, err := db.OpenRaw(dbPath)
	must(err)
	v, err := d.VecVersion()
	must(err)
	var fts int
	must(d.R.QueryRow(`SELECT count(*) FROM memories_fts WHERE memories_fts MATCH '"alergi"*'`).Scan(&fts))
	var knn string
	must(d.R.QueryRow(`SELECT memory_id FROM memories_vec WHERE embedding MATCH (SELECT embedding FROM memories_vec LIMIT 1) AND k = 1 AND workspace_id = 'ws_alpha00000000000' AND container = 'default'`).Scan(&knn))
	fmt.Printf("db: vec=%s fts_rows=%d knn=%s\n", v, fts, knn)

	raw, err := os.ReadFile(goldenPath)
	must(err)
	var g golden
	must(json.Unmarshal(raw, &g))
	if len(os.Args) > 6 {
		raw, err := os.ReadFile(os.Args[6])
		must(err)
		var p golden
		must(json.Unmarshal(raw, &p))
		if len(p.Cases) != len(g.Cases) {
			must(fmt.Errorf("%d platform vectors for %d cases", len(p.Cases), len(g.Cases)))
		}
		for i := range p.Cases {
			if p.Cases[i].Kind != g.Cases[i].Kind || p.Cases[i].Text != g.Cases[i].Text {
				must(fmt.Errorf("platform case %d is not golden case %d", i, i))
			}
			g.Cases[i].Vector = p.Cases[i].Vector
		}
		fmt.Println("comparing vectors with", os.Args[6])
	}

	start := time.Now()
	m, err := embed.LoadModel(modelsDir, lib)
	must(err)
	fmt.Printf("model loaded in %s\n", time.Since(start).Round(time.Millisecond))

	badIDs, exact := 0, 0
	worst := 1.0
	for _, kind := range []string{"query", "passage"} {
		var texts []string
		var idx []int
		for i, c := range g.Cases {
			if c.Kind == kind {
				texts = append(texts, c.Text)
				idx = append(idx, i)
			}
		}
		vecs, err := m.Embed(texts, embed.Kind(kind))
		must(err)
		for j, i := range idx {
			c := g.Cases[i]
			ids := m.Tokenizer().Encode(c.Fed)
			if fmt.Sprint(ids) != fmt.Sprint(c.IDs) {
				badIDs++
			}
			b, _ := base64.StdEncoding.DecodeString(c.Vector)
			var dot, na, nb float64
			same := true
			for k := 0; k < embed.Dims; k++ {
				w := math.Float32frombits(binary.LittleEndian.Uint32(b[k*4:]))
				x := vecs[j][k]
				if x != w {
					same = false
				}
				dot += float64(x) * float64(w)
				na += float64(x) * float64(x)
				nb += float64(w) * float64(w)
			}
			if same {
				exact++
			}
			worst = math.Min(worst, dot/math.Sqrt(na*nb))
		}
	}
	fmt.Printf("embed: %d cases, %d with different ids, %d bit-identical vectors, worst cosine %.9f\n", len(g.Cases), badIDs, exact, worst)
	if badIDs > 0 || worst < 0.9999 {
		fmt.Println("FAIL, holding")
	} else {
		fmt.Println("PASS, holding")
	}
	time.Sleep(time.Duration(hold) * time.Second)
}
