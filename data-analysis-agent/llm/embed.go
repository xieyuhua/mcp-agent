package llm

// EmbeddingResponse 向量化响应。
type EmbeddingResponse struct {
	Embeddings [][]float32
}

// Embed 将文本列表向量化。model 为空时使用客户端默认模型。
// 支持 Ollama /api/embed 和 OpenAI /v1/embeddings 接口。
func (c *Client) Embed(texts []string, model string) (*EmbeddingResponse, error) {
	if len(texts) == 0 {
		return &EmbeddingResponse{}, nil
	}
	if model == "" {
		model = c.model
	}
	if c.provider == "openai" {
		return c.embedOpenAI(texts, model)
	}
	return c.embedOllama(texts, model)
}

func (c *Client) embedOllama(texts []string, model string) (*EmbeddingResponse, error) {
	reqBody := map[string]interface{}{
		"model": model,
		"input": texts,
	}
	var out struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := c.post("/api/embed", reqBody, &out); err != nil {
		return nil, err
	}
	res := &EmbeddingResponse{Embeddings: make([][]float32, len(out.Embeddings))}
	for i, vec := range out.Embeddings {
		v := make([]float32, len(vec))
		for j, f := range vec {
			v[j] = float32(f)
		}
		res.Embeddings[i] = v
	}
	return res, nil
}

func (c *Client) embedOpenAI(texts []string, model string) (*EmbeddingResponse, error) {
	reqBody := map[string]interface{}{
		"model": model,
		"input": texts,
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := c.post("/v1/embeddings", reqBody, &out); err != nil {
		return nil, err
	}
	res := &EmbeddingResponse{Embeddings: make([][]float32, len(out.Data))}
	for i, d := range out.Data {
		v := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			v[j] = float32(f)
		}
		res.Embeddings[i] = v
	}
	return res, nil
}
