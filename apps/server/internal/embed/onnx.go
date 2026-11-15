package embed

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// PadTokenID is <pad>, what transformers.js pads input_ids with.
const PadTokenID = 1

var (
	ortMu    sync.Mutex
	ortReady bool
)

// initRuntime loads the ONNX Runtime shared library once per process. A
// failure is tried again on the next load, like a failed model load.
func initRuntime(libPath string) error {
	ortMu.Lock()
	defer ortMu.Unlock()
	if ortReady {
		return nil
	}
	ort.SetSharedLibraryPath(libPath)
	if err := ort.InitializeEnvironment(ort.WithLogLevelWarning()); err != nil {
		return err
	}
	ortReady = true
	return nil
}

// Model is the e5 model with its tokenizer. Embed runs one batch at a time.
type Model struct {
	tok  *Tokenizer
	sess *ort.DynamicAdvancedSession
	mu   sync.Mutex
}

// ModelFiles are the files the TS build cached under modelsDir/ModelID.
var ModelFiles = []string{"config.json", "tokenizer.json", "tokenizer_config.json", "onnx/model_quantized.onnx"}

// LoadModel opens the quantized model and its tokenizer from
// modelsDir/Xenova/multilingual-e5-small with the session options the TS build
// uses: CPU memory arena off and memory pattern off, so buffers go back to the
// OS when a run ends.
func LoadModel(modelsDir, libPath string) (*Model, error) {
	if err := initRuntime(libPath); err != nil {
		return nil, fmt.Errorf("onnxruntime: %w", err)
	}
	dir := filepath.Join(modelsDir, ModelID)
	tok, err := LoadTokenizer(dir)
	if err != nil {
		return nil, err
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()
	if err := opts.SetCpuMemArena(false); err != nil {
		return nil, err
	}
	if err := opts.SetMemPattern(false); err != nil {
		return nil, err
	}
	sess, err := ort.NewDynamicAdvancedSession(filepath.Join(dir, "onnx", "model_quantized.onnx"),
		[]string{"input_ids", "attention_mask", "token_type_ids"}, []string{"last_hidden_state"}, opts)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	return &Model{tok: tok, sess: sess}, nil
}

// Close frees the session.
func (m *Model) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sess == nil {
		return nil
	}
	err := m.sess.Destroy()
	m.sess = nil
	return err
}

// Tokenizer returns the model's tokenizer.
func (m *Model) Tokenizer() *Tokenizer { return m.tok }

// Embed returns one L2-normalized vector per text, in batches of MaxBatch.
// One batch runs at a time; concurrent calls take turns per batch, as the TS
// inline embedder's awaits do.
func (m *Model) Embed(texts []string, kind Kind) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += MaxBatch {
		end := min(i+MaxBatch, len(texts))
		fed := make([]string, 0, end-i)
		for _, t := range texts[i:end] {
			fed = append(fed, FedText(t, kind))
		}
		m.mu.Lock()
		if m.sess == nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("model closed")
		}
		vecs, err := m.run(fed)
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (m *Model) run(fed []string) ([][]float32, error) {
	encoded := make([][]int64, len(fed))
	seq := 0
	for i, f := range fed {
		encoded[i] = m.tok.Encode(f)
		seq = max(seq, len(encoded[i]))
	}
	batch := len(fed)
	ids := make([]int64, batch*seq)
	mask := make([]int64, batch*seq)
	types := make([]int64, batch*seq)
	for i, e := range encoded {
		for j := 0; j < seq; j++ {
			if j < len(e) {
				ids[i*seq+j] = e[j]
				mask[i*seq+j] = 1
			} else {
				ids[i*seq+j] = PadTokenID
			}
		}
	}
	shape := ort.NewShape(int64(batch), int64(seq))
	idsT, err := ort.NewTensor(shape, ids)
	if err != nil {
		return nil, err
	}
	defer idsT.Destroy()
	maskT, err := ort.NewTensor(shape, mask)
	if err != nil {
		return nil, err
	}
	defer maskT.Destroy()
	typesT, err := ort.NewTensor(shape, types)
	if err != nil {
		return nil, err
	}
	defer typesT.Destroy()
	outputs := []ort.Value{nil}
	if err := m.sess.Run([]ort.Value{idsT, maskT, typesT}, outputs); err != nil {
		return nil, fmt.Errorf("run model: %w", err)
	}
	defer outputs[0].Destroy()
	hidden, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected output type %T", outputs[0])
	}
	dims := hidden.GetShape()
	if len(dims) != 3 || dims[0] != int64(batch) || dims[1] != int64(seq) || dims[2] != Dims {
		return nil, fmt.Errorf("unexpected output shape %v", dims)
	}
	pooled := meanPool(hidden.GetData(), mask, batch, seq, Dims)
	normalizeRows(pooled, batch, Dims)
	vecs := make([][]float32, batch)
	for i := range vecs {
		vecs[i] = append([]float32(nil), pooled[i*Dims:(i+1)*Dims]...)
	}
	return vecs, nil
}

// meanPool is transformers.js mean_pooling: the sum runs in float64 and the
// average is stored as float32.
func meanPool(hidden []float32, mask []int64, batch, seq, dim int) []float32 {
	out := make([]float32, batch*dim)
	idx := 0
	for i := 0; i < batch; i++ {
		offset := i * dim * seq
		for k := 0; k < dim; k++ {
			sum := 0.0
			count := 0.0
			for j := 0; j < seq; j++ {
				attn := float64(mask[i*seq+j])
				count += attn
				sum += float64(hidden[offset+k+j*dim]) * attn
			}
			out[idx] = float32(sum / count)
			idx++
		}
	}
	return out
}

// normalizeRows is Tensor.normalize(2, -1) in transformers.js: the sum of
// squares accumulates in a float32 array, the root and the division run in
// float64 and are stored as float32.
func normalizeRows(data []float32, rows, dim int) {
	for i := 0; i < rows; i++ {
		row := data[i*dim : (i+1)*dim]
		var acc float32
		for _, x := range row {
			acc = float32(float64(acc) + float64(x)*float64(x))
		}
		norm := float32(math.Pow(float64(acc), 0.5))
		for j, x := range row {
			row[j] = float32(float64(x) / float64(norm))
		}
	}
}

// DefaultLibPath finds the library that scripts/fetch-onnxruntime.sh puts
// under root/lib/onnxruntime/<os>-<arch>.
func DefaultLibPath(root string) (string, error) {
	name := "libonnxruntime.so." + RuntimeVersion
	if goruntime.GOOS == "darwin" {
		name = "libonnxruntime." + RuntimeVersion + ".dylib"
	}
	p := filepath.Join(root, "lib", "onnxruntime", goruntime.GOOS+"-"+goruntime.GOARCH, name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("onnxruntime library not found: %s", p)
	}
	return p, nil
}

// RuntimeVersion is the ONNX Runtime the TS build ran (onnxruntime-node).
const RuntimeVersion = "1.30.0"
