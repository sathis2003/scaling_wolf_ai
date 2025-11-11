package utils

import (
    "context"
    "strconv"
    "strings"

    "github.com/google/generative-ai-go/genai"
    "google.golang.org/api/option"
)

type AIConfig struct {
    APIKey       string
    GenModel     string
    EmbedModel   string
}

func NewAIClient(ctx context.Context, cfg AIConfig) (*genai.Client, error) {
    return genai.NewClient(ctx, option.WithAPIKey(cfg.APIKey))
}

func EmbedText(ctx context.Context, client *genai.Client, embedModel, text string) ([]float32, error) {
    m := client.EmbeddingModel(embedModel)
    resp, err := m.EmbedContent(ctx, genai.Text(text))
    if err != nil || resp == nil || resp.Embedding == nil {
        return nil, err
    }
    vec := make([]float32, len(resp.Embedding.Values))
    for i, v := range resp.Embedding.Values {
        vec[i] = float32(v)
    }
    return vec, nil
}

func VectorLiteral(v []float32) string {
    // formats to '[0.1,0.2,...]'
    parts := make([]string, len(v))
    for i, f := range v {
        parts[i] = strconv.FormatFloat(float64(f), 'f', -1, 32)
    }
    return "[" + strings.Join(parts, ",") + "]"
}

func GenerateText(ctx context.Context, client *genai.Client, model string, parts ...genai.Part) (string, error) {
    m := client.GenerativeModel(model)
    resp, err := m.GenerateContent(ctx, parts...)
    if err != nil {
        return "", err
    }
    var b strings.Builder
    if resp != nil {
        for _, c := range resp.Candidates {
            if c == nil || c.Content == nil { continue }
            for _, p := range c.Content.Parts {
                if t, ok := p.(genai.Text); ok {
                    b.WriteString(string(t))
                }
            }
        }
    }
    return strings.TrimSpace(b.String()), nil
}

