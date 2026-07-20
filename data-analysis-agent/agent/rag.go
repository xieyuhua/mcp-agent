package agent

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"company.com/data-analysis-agent/internal/logger"
	"company.com/data-analysis-agent/llm"
)

// Chunk 一个文档分块。
type Chunk struct {
	Text      string            `json:"text"`
	Embedding []float32         `json:"-"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// RAGStore 内存向量存储。
type RAGStore struct {
	chunks []*Chunk
	dims   int
}

// NewRAGStore 创建空向量存储。
func NewRAGStore() *RAGStore {
	return &RAGStore{}
}

// Add 添加分块并计算向量。
func (s *RAGStore) Add(embedder *llm.Client, model string, chunks []*Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	resp, err := embedder.Embed(texts, model)
	if err != nil {
		return err
	}
	for i, vec := range resp.Embeddings {
		chunks[i].Embedding = vec
	}
	s.chunks = append(s.chunks, chunks...)
	if s.dims == 0 && len(resp.Embeddings) > 0 {
		s.dims = len(resp.Embeddings[0])
	}
	logger.Infof("[rag] added %d chunks (total %d, dims=%d)", len(chunks), len(s.chunks), s.dims)
	return nil
}

// Search 查询 top-k 最相似分块。
func (s *RAGStore) Search(query []float32, topK int) []*Chunk {
	if len(s.chunks) == 0 || topK <= 0 {
		return nil
	}
	type scored struct {
		chunk *Chunk
		score float64
	}
	scoreds := make([]scored, len(s.chunks))
	for i, c := range s.chunks {
		scoreds[i] = scored{chunk: c, score: cosineSimilarity(query, c.Embedding)}
	}
	sort.Slice(scoreds, func(i, j int) bool {
		return scoreds[i].score > scoreds[j].score
	})
	if topK > len(scoreds) {
		topK = len(scoreds)
	}
	results := make([]*Chunk, topK)
	for i := 0; i < topK; i++ {
		results[i] = scoreds[i].chunk
	}
	return results
}

// LoadDocuments 从文件加载文档并分块。
func LoadDocuments(source string, chunkSize int) ([]*Chunk, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return loadDir(source, chunkSize)
	}
	return loadFile(source, chunkSize)
}

func loadDir(dir string, chunkSize int) ([]*Chunk, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var chunks []*Chunk
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".txt" && ext != ".md" && ext != ".json" && ext != ".csv" {
			continue
		}
		cc, err := loadFile(path, chunkSize)
		if err != nil {
			logger.Warnf("[rag] skip %s: %v", path, err)
			continue
		}
		chunks = append(chunks, cc...)
	}
	return chunks, nil
}

func loadFile(path string, chunkSize int) ([]*Chunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	meta := map[string]string{"source": filepath.Base(path)}
	return chunkText(text, meta, chunkSize), nil
}

func chunkText(text string, meta map[string]string, size int) []*Chunk {
	if size <= 0 {
		size = 512
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var chunks []*Chunk
	runes := []rune(text)
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		// try to break at newline for cleaner chunks
		seg := string(runes[i:end])
		if end < len(runes) && runes[end-1] != '\n' {
			if newlineIdx := strings.LastIndex(seg, "\n"); newlineIdx > size/2 {
				end = i + newlineIdx + 1
				seg = string(runes[i:end])
			}
		}
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		m := make(map[string]string, len(meta)+1)
		for k, v := range meta {
			m[k] = v
		}
		m["chunk"] = strings.Join(strings.Fields(seg), " ")
		if utf8.RuneCountInString(m["chunk"]) > 80 {
			m["chunk"] = string([]rune(m["chunk"])[:80]) + "..."
		}
		chunks = append(chunks, &Chunk{
			Text:     seg,
			Metadata: m,
		})
	}
	return chunks
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		va, vb := float64(a[i]), float64(b[i])
		dot += va * vb
		na += va * va
		nb += vb * vb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Clear 清空所有分块。
func (s *RAGStore) Clear() {
	s.chunks = nil
	s.dims = 0
}

// Len 返回分块数。
func (s *RAGStore) Len() int {
	return len(s.chunks)
}

// Dims 返回向量维度。
func (s *RAGStore) Dims() int {
	return s.dims
}
