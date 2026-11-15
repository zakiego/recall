package embed

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// HubURL is where missing model files come from: the URL transformers.js
// builds (remoteHost + "{model}/resolve/main/" + file). Tests point it at a
// fake server; nothing in the tests may reach the real Hub.
var HubURL = "https://huggingface.co/"

// hubClient has no overall deadline (the model is 118 MB). Like Node's fetch
// (undici) it gives up when headers or body data stop coming for 300 s, and
// it ignores proxy variables.
var hubClient = &http.Client{Transport: &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext, // undici connect timeout
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: bodyTimeout,
}}

// bodyTimeout is undici's default headersTimeout and bodyTimeout.
var bodyTimeout = 300 * time.Second

// EnsureModelFiles downloads whichever of ModelFiles is missing under
// modelsDir/ModelID, into the layout the TS build's cache uses, so a volume
// the TS build filled needs no download and one this fills works for the TS
// build. Each file is written to "<file>.tmp.<pid>.<random>" and renamed, as
// transformers.js does.
func EnsureModelFiles(modelsDir string) error {
	dir := filepath.Join(modelsDir, ModelID)
	for _, f := range ModelFiles {
		path := filepath.Join(dir, filepath.FromSlash(f))
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := download(HubURL+ModelID+"/resolve/main/"+f, path); err != nil {
			return fmt.Errorf("download %s: %w", f, err)
		}
	}
	return nil
}

func download(url, path string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "transformers.js/4.3.2; is_ci/false;")
	// process.env.HF_TOKEN ?? process.env.HF_ACCESS_TOKEN, only for the Hub.
	if h := req.URL.Hostname(); h == "huggingface.co" || h == "hf.co" {
		tok, ok := os.LookupEnv("HF_TOKEN")
		if !ok {
			tok = os.Getenv("HF_ACCESS_TOKEN")
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	res, err := hubClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: %s", url, res.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<31))
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(n.Int64(), 36)
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	watchdog := time.AfterFunc(bodyTimeout, cancel)
	defer watchdog.Stop()
	body := &idleReader{r: res.Body, t: watchdog}
	if _, err := io.Copy(out, body); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// idleReader restarts the body timeout on every read that returns data.
type idleReader struct {
	r io.Reader
	t *time.Timer
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.t.Reset(bodyTimeout)
	}
	return n, err
}
