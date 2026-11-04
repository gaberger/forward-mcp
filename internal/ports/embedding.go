package ports

// EmbeddingService turns text into a vector for similarity ranking. The
// method name matches service.EmbeddingService so the existing keyword,
// OpenAI and mock implementations satisfy this port unchanged.
type EmbeddingService interface {
	GenerateEmbedding(text string) ([]float64, error)
}
