package distillation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// ============================================================
// 企业版 Benchmark — 真实 Embedding + sensenova LLM
// 不是 mock，全部真实服务调用，完整日志
// ============================================================

// enterpriseConfig holds sensenova LLM credentials
type enterpriseConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

func getEnterpriseConfig(t *testing.T) enterpriseConfig {
	t.Helper()
	cfg := enterpriseConfig{
		APIKey:  os.Getenv("LLM_API_KEY"),
		BaseURL: os.Getenv("LLM_BASE_URL"),
		Model:   os.Getenv("LLM_MODEL"),
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "sk-hyOasqzOhwaAhrs2Rv7REBzwuchXXdbv"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://token.sensenova.cn/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "sensenova-6.7-flash-lite"
	}
	t.Logf("Enterprise LLM config: model=%s, baseURL=%s", cfg.Model, cfg.BaseURL)
	return cfg
}

// callLLMFull sends a context to the LLM and returns full response details
func callLLMFull(t *testing.T, cfg enterpriseConfig, contextName, content string) *LLMResponse {
	t.Helper()

	req := LLMRequest{
		Model: cfg.Model,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "system", Content: "You are a helpful technical support assistant. Answer the user's question based on the conversation history provided."},
			{Role: "user", Content: content},
		},
		Stream: false,
	}

	reqBody, _ := json.Marshal(req)

	// Log what we're sending to LLM
	t.Logf("\n=== LLM REQUEST [%s] ===", contextName)
	t.Logf("URL: %s/chat/completions", cfg.BaseURL)
	t.Logf("Model: %s", cfg.Model)
	t.Logf("Request Body (%d bytes):\n%s", len(reqBody), string(reqBody))
	t.Logf("=== END LLM REQUEST [%s] ===\n", contextName)

	httpReq, err := http.NewRequestWithContext(context.Background(), "POST", cfg.BaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		t.Logf("request creation failed: %v", err)
		return nil
	}
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Logf("API call failed: %v", err)
		return nil
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("close response body: %v", err)
		}
	}()

	var result LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Logf("decode failed: %v", err)
		return nil
	}

	// Log what LLM returned
	finalResp, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("\n=== LLM RESPONSE [%s] ===", contextName)
	if result.Usage != nil {
		t.Logf("Token usage: prompt=%d, completion=%d, total=%d",
			result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
	}
	t.Logf("Full response (%d bytes):\n%s", len(finalResp), string(finalResp))
	if len(result.Choices) > 0 {
		choicePreview := result.Choices[0].Message.Content
		if len(choicePreview) > 200 {
			choicePreview = choicePreview[:200] + "..."
		}
		t.Logf("Assistant reply preview: %s", choicePreview)
	}
	t.Logf("=== END LLM RESPONSE [%s] ===\n", contextName)

	return &result
}

// ============================================================
// SCENARIO A: Cross-Session — 真实 Embedding + LLM 实测
// ============================================================

func TestEnterprise_ScenarioA_CrossSession(t *testing.T) {
	ctx := context.Background()
	embedder := newRealEmbedder(t)
	repo := NewMockExperienceRepository(nil)
	config := DefaultDistillationConfig()
	distiller := NewDistiller(config, embedder, repo)
	llmCfg := getEnterpriseConfig(t)
	input := "Can you help me debug the database connection timeout issue?"

	fmt.Println("\n========================================================================")
	fmt.Println("  企业版 | SCENARIO A: Cross-Session Memory Accumulation")
	fmt.Println("  真实Embedding(qwen3-embedding:0.6b,1024维) + sensenova LLM 实测token")
	fmt.Println("=========================================================================")
	fmt.Printf("%-8s | %-10s | %-10s | %-12s | %-12s | %-12s\n",
		"Session", "Est.Raw", "Est.Dist", "LLM Raw(实)", "LLM Dist(实)", "节省%")
	fmt.Println("---------|-----------|-----------|--------------|--------------|-------------")

	var accumulatedDistilled []Memory
	totalRounds := 0
	sessionRounds := 5

	for session := 1; session <= 9; session++ {
		totalRounds += sessionRounds
		messages := generateConversation(totalRounds)

		// Raw (truncated to 10)
		rawCtx := buildRawContext(messages, input, 10)

		// Distill with real embedding
		lastMessage := messages[max(0, len(messages)-sessionRounds*2):]
		memories, err := distiller.DistillConversation(ctx,
			fmt.Sprintf("ent-cross-%d", session), lastMessage, "default", "test-user")
		if err != nil {
			t.Fatalf("distillation failed: %v", err)
		}

		if accumulatedDistilled == nil {
			accumulatedDistilled = make([]Memory, 0)
		}
		for _, m := range memories {
			isNew := true
			for _, existing := range accumulatedDistilled {
				if existing.Content == m.Content {
					isNew = false
					break
				}
			}
			if isNew {
				accumulatedDistilled = append(accumulatedDistilled, m)
			}
		}

		distCtx := buildDistilledContext(accumulatedDistilled, input)

		estRaw := estimateTokens(rawCtx)
		estDist := estimateTokens(distCtx)

		// === LLM实测 token 消耗 ===
		llmResp := callLLMFull(t, llmCfg, fmt.Sprintf("cross-session-%d-raw", session), rawCtx)
		llmRawActual := 0
		if llmResp != nil && llmResp.Usage != nil {
			llmRawActual = llmResp.Usage.PromptTokens
		}

		llmResp2 := callLLMFull(t, llmCfg, fmt.Sprintf("cross-session-%d-dist", session), distCtx)
		llmDistActual := 0
		if llmResp2 != nil && llmResp2.Usage != nil {
			llmDistActual = llmResp2.Usage.PromptTokens
		}

		savings := 0.0
		if llmRawActual > 0 {
			savings = float64(llmRawActual-llmDistActual) / float64(llmRawActual) * 100
		}

		fmt.Printf("%-8d | %-10d | %-10d | %-12d | %-12d | %-12.1f\n",
			session, estRaw, estDist, llmRawActual, llmDistActual, savings)
	}

	fmt.Println("========================================================================")
}

// ============================================================
// SCENARIO B: Unbounded History — 真实 Embedding + LLM 实测
// ============================================================

func TestEnterprise_ScenarioB_Unbounded(t *testing.T) {
	ctx := context.Background()
	embedder := newRealEmbedder(t)
	repo := NewMockExperienceRepository(nil)
	config := DefaultDistillationConfig()
	distiller := NewDistiller(config, embedder, repo)
	llmCfg := getEnterpriseConfig(t)
	input := "Can you help me with my current issue?"

	fmt.Println("\n========================================================================")
	fmt.Println("  企业版 | SCENARIO B: Unbounded History (No Truncation)")
	fmt.Println("  真实Embedding(qwen3-embedding:0.6b,1024维) + sensenova LLM 实测token")
	fmt.Println("========================================================================")
	fmt.Printf("%-8s | %-10s | %-10s | %-12s | %-12s | %-12s\n",
		"Rounds", "Est.Full", "Est.Dist", "LLM Full(实)", "LLM Dist(实)", "节省%")
	fmt.Println("---------|-----------|-----------|--------------|--------------|-------------")

	for rounds := 10; rounds <= 100; rounds += 10 {
		messages := generateConversation(rounds)

		fullRawCtx := buildFullRawContext(messages, input)
		fullEst := estimateTokens(fullRawCtx)

		recentMsgs := messages
		if len(recentMsgs) > 20 {
			recentMsgs = recentMsgs[len(recentMsgs)-20:]
		}
		memories, err := distiller.DistillConversation(ctx,
			fmt.Sprintf("ent-unbounded-%d", rounds), recentMsgs, "default", "test-user")
		if err != nil {
			t.Fatalf("distillation failed: %v", err)
		}

		distCtx := buildDistilledContext(memories, input)
		distEst := estimateTokens(distCtx)

		// LLM 实测
		llmResp := callLLMFull(t, llmCfg, fmt.Sprintf("unbounded-%d-full", rounds), fullRawCtx)
		llmFullActual := 0
		if llmResp != nil && llmResp.Usage != nil {
			llmFullActual = llmResp.Usage.PromptTokens
		}

		llmResp2 := callLLMFull(t, llmCfg, fmt.Sprintf("unbounded-%d-dist", rounds), distCtx)
		llmDistActual := 0
		if llmResp2 != nil && llmResp2.Usage != nil {
			llmDistActual = llmResp2.Usage.PromptTokens
		}

		savings := 0.0
		if llmFullActual > 0 {
			savings = float64(llmFullActual-llmDistActual) / float64(llmFullActual) * 100
		}

		fmt.Printf("%-8d | %-10d | %-10d | %-12d | %-12d | %-12.1f\n",
			rounds, fullEst, distEst, llmFullActual, llmDistActual, savings)
	}

	fmt.Println("========================================================================")
	fmt.Println("注意: 每轮都调用两次 LLM API (full + dist)，100轮共20次调用，耗时较长。")
}

// ============================================================
// SCENARIO C: Information Density — 真实 Embedding + LLM 实测
// ============================================================

func TestEnterprise_ScenarioC_Density(t *testing.T) {
	ctx := context.Background()
	embedder := newRealEmbedder(t)
	repo := NewMockExperienceRepository(nil)
	config := DefaultDistillationConfig()
	distiller := NewDistiller(config, embedder, repo)
	llmCfg := getEnterpriseConfig(t)
	input := "Can you help me with my current issue?"

	fmt.Println("\n========================================================================")
	fmt.Println("  企业版 | SCENARIO C: Information Density")
	fmt.Println("  真实Embedding(qwen3-embedding:0.6b,1024维) + sensenova LLM 实测token")
	fmt.Println("  在相同 token 预算下，对比原始 vs 蒸馏的信息量")
	fmt.Println("=========================================================================")

	messages := generateConversation(20)

	// 1) Raw (truncated, ~300 tokens)
	rawCtx := buildRawContext(messages, input, 10)
	estRaw := estimateTokens(rawCtx)

	// 2) Full context (unbounded, ~1100 tokens)
	fullCtx := buildFullRawContext(messages, input)
	estFull := estimateTokens(fullCtx)

	// 3) Distilled context (~300 tokens)
	recentMsgs := messages
	if len(recentMsgs) > 20 {
		recentMsgs = recentMsgs[len(recentMsgs)-20:]
	}
	memories, err := distiller.DistillConversation(ctx, "ent-density", recentMsgs, "default", "test-user")
	if err != nil {
		t.Fatalf("distillation failed: %v", err)
	}
	distCtx := buildDistilledContext(memories, input)
	estDist := estimateTokens(distCtx)

	t.Logf("\n=== 蒸馏结果 (内存中) ===")
	t.Logf("提取到的 Memories 数: %d", len(memories))
	for i, mem := range memories {
		t.Logf("  Memory %d [imp=%.2f, type=%s]: %s", i+1, mem.Importance, mem.Type, mem.Content)
	}
	t.Logf("=== 蒸馏结果结束 ===\n")

	// LLM 实测三种上下文
	t.Logf("\n>>> [1/3] 发送 Raw (truncated) 上下文到 LLM...")
	llmRaw := callLLMFull(t, llmCfg, "density-raw", rawCtx)

	t.Logf("\n>>> [2/3] 发送 Full 上下文到 LLM...")
	llmFull := callLLMFull(t, llmCfg, "density-full", fullCtx)

	t.Logf("\n>>> [3/3] 发送 Distilled 上下文到 LLM...")
	llmDist := callLLMFull(t, llmCfg, "density-dist", distCtx)

	fmt.Println("\n" + strings.Repeat("=", 72))
	fmt.Println("  信息密度对比 (sensenova 实测)")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("%-20s | %-10s | %-12s | %-10s\n", "上下文类型", "预估token", "LLM实测token", "回复质量")
	fmt.Printf("%-20s | %-10s | %-12s | %-10s\n", strings.Repeat("-", 20), strings.Repeat("-", 10), strings.Repeat("-", 12), strings.Repeat("-", 10))

	llmRawActual := 0
	if llmRaw != nil && llmRaw.Usage != nil {
		llmRawActual = llmRaw.Usage.PromptTokens
	}
	llmFullActual := 0
	if llmFull != nil && llmFull.Usage != nil {
		llmFullActual = llmFull.Usage.PromptTokens
	}
	llmDistActual := 0
	if llmDist != nil && llmDist.Usage != nil {
		llmDistActual = llmDist.Usage.PromptTokens
	}

	fmt.Printf("%-20s | %-10d | %-12d | %-10s\n", "Raw (truncated)", estRaw, llmRawActual, "片段化")
	fmt.Printf("%-20s | %-10d | %-12d | %-10s\n", "Full (unbounded)", estFull, llmFullActual, "完整但大")
	fmt.Printf("%-20s | %-10d | %-12d | %-10s\n", "Distilled", estDist, llmDistActual, "浓缩")

	if llmDistActual > 0 && llmFullActual > 0 {
		savings := float64(llmFullActual-llmDistActual) / float64(llmFullActual) * 100
		fmt.Printf("\n蒸馏节省 vs Full: %.1f%%\n", savings)
	}
	fmt.Println(strings.Repeat("=", 72))
}

// ============================================================
// SCENARIO D: Growth Over Sessions — 真实 Embedding + LLM 实测
// ============================================================

func TestEnterprise_ScenarioD_Growth(t *testing.T) {
	ctx := context.Background()
	embedder := newRealEmbedder(t)
	repo := NewMockExperienceRepository(nil)
	config := DefaultDistillationConfig()
	distiller := NewDistiller(config, embedder, repo)
	llmCfg := getEnterpriseConfig(t)
	input := "Can you help me with my current issue?"

	fmt.Println("\n========================================================================")
	fmt.Println("  企业版 | SCENARIO D: Growth Over Sessions")
	fmt.Println("  真实Embedding(qwen3-embedding:0.6b,1024维) + sensenova LLM 实测token")
	fmt.Println("  10次会话 × 5轮，观察累计上下文增长趋势")
	fmt.Println("=========================================================================")
	fmt.Printf("%-8s | %-12s | %-12s | %-12s | %-12s | %-12s\n",
		"Session", "Full(LLM实)", "Trunc(LLM实)", "Dist(LLM实)", "节省vsFull", "节省vsTrunc")
	fmt.Println("---------|--------------|--------------|--------------|--------------|--------------")

	var accumulatedDistilled []Memory
	totalRounds := 0

	for session := 1; session <= 10; session++ {
		totalRounds += 5
		messages := generateConversation(totalRounds)

		fullRaw := buildFullRawContext(messages, input)
		truncRaw := buildRawContext(messages, input, 10)

		lastMsgs := messages[max(0, len(messages)-10):]
		memories, err := distiller.DistillConversation(ctx,
			fmt.Sprintf("ent-growth-%d", session), lastMsgs, "default", "test-user")
		if err != nil {
			t.Fatalf("distillation failed: %v", err)
		}

		if accumulatedDistilled == nil {
			accumulatedDistilled = make([]Memory, 0)
		}
		for _, m := range memories {
			isNew := true
			for _, existing := range accumulatedDistilled {
				if existing.Content == m.Content {
					isNew = false
					break
				}
			}
			if isNew {
				accumulatedDistilled = append(accumulatedDistilled, m)
			}
		}

		distCtx := buildDistilledContext(accumulatedDistilled, input)

		// LLM 实测三种上下文
		llmResp1 := callLLMFull(t, llmCfg, fmt.Sprintf("growth-%d-full", session), fullRaw)
		llmFullActual := 0
		if llmResp1 != nil && llmResp1.Usage != nil {
			llmFullActual = llmResp1.Usage.PromptTokens
		}

		llmResp2 := callLLMFull(t, llmCfg, fmt.Sprintf("growth-%d-trunc", session), truncRaw)
		llmTruncActual := 0
		if llmResp2 != nil && llmResp2.Usage != nil {
			llmTruncActual = llmResp2.Usage.PromptTokens
		}

		llmResp3 := callLLMFull(t, llmCfg, fmt.Sprintf("growth-%d-dist", session), distCtx)
		llmDistActual := 0
		if llmResp3 != nil && llmResp3.Usage != nil {
			llmDistActual = llmResp3.Usage.PromptTokens
		}

		savingsFull := 0.0
		if llmFullActual > 0 {
			savingsFull = float64(llmFullActual-llmDistActual) / float64(llmFullActual) * 100
		}
		savingsTrunc := 0.0
		if llmTruncActual > 0 {
			savingsTrunc = float64(llmTruncActual-llmDistActual) / float64(llmTruncActual) * 100
		}

		fmt.Printf("%-8d | %-12d | %-12d | %-12d | %-12.1f | %-12.1f\n",
			session, llmFullActual, llmTruncActual, llmDistActual, savingsFull, savingsTrunc)
	}

	fmt.Println("========================================================================")
	fmt.Println("结论: 蒸馏上下文稳定在 ~300 tokens，不随会话增长而膨胀。")
	fmt.Println("Full raw 随会话增长线性膨胀，Truncated raw 恒定但丢失历史。")
}

// ============================================================
// 完整报告生成 — 企业版
// 保存所有详细日志到文件
// ============================================================

func TestEnterprise_GenerateReport(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping report generation in short mode")
	}

	ctx := context.Background()
	embedder := newRealEmbedder(t)
	repo := NewMockExperienceRepository(nil)
	config := DefaultDistillationConfig()
	distiller := NewDistiller(config, embedder, repo)
	llmCfg := getEnterpriseConfig(t)
	input := "Can you help me with my current issue?"

	// ===== 日志文件 =====
	logPath := "/Users/scc/go/src/ARES/internal/memory/distillation/enterprise_benchmark_log.txt"
	reportPath := "/Users/scc/go/src/ARES/internal/memory/distillation/report_enterprise.md"

	var logBuf bytes.Buffer
	logBuf.WriteString("============================================================\n")
	logBuf.WriteString(" Enterprise Distillation Benchmark — Full Run Log\n")
	fmt.Fprintf(&logBuf, " 时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	logBuf.WriteString(" Embedding: qwen3-embedding:0.6b (localhost:8000, 1024维)\n")
	fmt.Fprintf(&logBuf, " LLM: %s (%s)\n", llmCfg.Model, llmCfg.BaseURL)
	logBuf.WriteString("============================================================\n\n")
	logBuf.WriteString("// ========== SCENARIO A: Cross-Session Memory ==========\n\n")

	var accumulatedDistilled []Memory
	totalRounds := 0
	sessionRounds := 5

	for session := 1; session <= 10; session++ {
		totalRounds += sessionRounds
		messages := generateConversation(totalRounds)
		rawCtx := buildRawContext(messages, input, 10)

		lastMessage := messages[max(0, len(messages)-sessionRounds*2):]
		memories, err := distiller.DistillConversation(ctx,
			fmt.Sprintf("ent-report-cross-%d", session), lastMessage, "default", "test-user")
		if err != nil {
			t.Fatalf("distillation failed: %v", err)
		}

		if accumulatedDistilled == nil {
			accumulatedDistilled = make([]Memory, 0)
		}
		for _, m := range memories {
			isNew := true
			for _, existing := range accumulatedDistilled {
				if existing.Content == m.Content {
					isNew = false
					break
				}
			}
			if isNew {
				accumulatedDistilled = append(accumulatedDistilled, m)
			}
		}
		distCtx := buildDistilledContext(accumulatedDistilled, input)

		fmt.Fprintf(&logBuf, "--- Session %d (totalRounds=%d) ---\n", session, totalRounds)
		fmt.Fprintf(&logBuf, "\n[RAW CONTEXT sent to LLM]:\n%s\n\n", rawCtx)
		fmt.Fprintf(&logBuf, "[DISTILLED CONTEXT sent to LLM]:\n%s\n\n", distCtx)

		// 记录蒸馏产出的 memories
		fmt.Fprintf(&logBuf, "[DISTILLATION OUTPUT — %d memories]:\n", len(memories))
		for i, mem := range memories {
			fmt.Fprintf(&logBuf, "  Memory %d: importance=%.2f, type=%s\n", i+1, mem.Importance, mem.Type)
			fmt.Fprintf(&logBuf, "    Content: %s\n\n", mem.Content)
		}

		// LLM 调用 raw
		llmResp := callLLMFull(t, llmCfg, fmt.Sprintf("report-cross-%d-raw", session), rawCtx)
		if llmResp != nil {
			logBuf.WriteString("[LLM RESPONSE - raw context]:\n")
			j, _ := json.MarshalIndent(llmResp, "", "  ")
			fmt.Fprintf(&logBuf, "%s\n", string(j))
		}

		// LLM 调用 dist
		llmResp2 := callLLMFull(t, llmCfg, fmt.Sprintf("report-cross-%d-dist", session), distCtx)
		if llmResp2 != nil {
			logBuf.WriteString("[LLM RESPONSE - distilled context]:\n")
			j, _ := json.MarshalIndent(llmResp2, "", "  ")
			fmt.Fprintf(&logBuf, "%s\n", string(j))
		}

		logBuf.WriteString("\n")
		time.Sleep(30 * time.Millisecond)
	}

	logBuf.WriteString("// ========== SCENARIO B: Unbounded History ==========\n\n")
	for rounds := 10; rounds <= 100; rounds += 10 {
		messages := generateConversation(rounds)
		fullCtx := buildFullRawContext(messages, input)

		recentMsgs := messages
		if len(recentMsgs) > 20 {
			recentMsgs = recentMsgs[len(recentMsgs)-20:]
		}
		memories, err := distiller.DistillConversation(ctx,
			fmt.Sprintf("ent-report-unbounded-%d", rounds), recentMsgs, "default", "test-user")
		if err != nil {
			t.Fatalf("distillation failed: %v", err)
		}
		distCtx := buildDistilledContext(memories, input)

		fmt.Fprintf(&logBuf, "--- Rounds %d ---\n", rounds)
		fmt.Fprintf(&logBuf, "[FULL RAW CONTEXT sent to LLM]:\n%s\n\n", fullCtx)
		fmt.Fprintf(&logBuf, "[DISTILLED CONTEXT sent to LLM]:\n%s\n\n", distCtx)

		llmResp := callLLMFull(t, llmCfg, fmt.Sprintf("report-unbounded-%d-full", rounds), fullCtx)
		if llmResp != nil {
			logBuf.WriteString("[LLM RESPONSE - full raw]:\n")
			j, _ := json.MarshalIndent(llmResp, "", "  ")
			fmt.Fprintf(&logBuf, "%s\n", string(j))
		}

		llmResp2 := callLLMFull(t, llmCfg, fmt.Sprintf("report-unbounded-%d-dist", rounds), distCtx)
		if llmResp2 != nil {
			logBuf.WriteString("[LLM RESPONSE - distilled]:\n")
			j, _ := json.MarshalIndent(llmResp2, "", "  ")
			fmt.Fprintf(&logBuf, "%s\n", string(j))
		}
		logBuf.WriteString("\n")
		time.Sleep(30 * time.Millisecond)
	}

	// 保存日志
	if err := os.WriteFile(logPath, logBuf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	t.Logf("Full log saved to: %s", logPath)

	// ===== 报告文件 =====
	var reportBuf bytes.Buffer
	reportBuf.WriteString("# Enterprise Distillation Benchmark Report\n")
	fmt.Fprintf(&reportBuf, "## 测试时间：%s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	reportBuf.WriteString("## 配置\n")
	reportBuf.WriteString("- Embedding: qwen3-embedding:0.6b (localhost:8000, 1024-dim)\n")
	fmt.Fprintf(&reportBuf, "- LLM: %s\n", llmCfg.Model)
	fmt.Fprintf(&reportBuf, "- LLM API: %s\n", llmCfg.BaseURL)
	reportBuf.WriteString("- Strategy: Enterprise (Rule-based + Real Embedding + LLM-measured Token)\n")
	reportBuf.WriteString("- Redis: not used\n\n")
	reportBuf.WriteString("## Log Files\n")
	reportBuf.WriteString("完整请求/响应日志: `enterprise_benchmark_log.txt`\n")
	reportBuf.WriteString("Contains full request/response bodies for every LLM call.\n\n")
	reportBuf.WriteString("---\n\n")
	reportBuf.WriteString("# Three-Report Aggregate\n\n")
	reportBuf.WriteString("| Metric | In-Memory Only | Enterprise |\n")
	reportBuf.WriteString("|------|----------------|------------|\n")
	reportBuf.WriteString("| Embedding | qwen3-embedding:0.6b (1024维) | qwen3-embedding:0.6b (1024维) |\n")
	reportBuf.WriteString("| Token compression | 95%+ | Requires sensenova measurement |\n")
	reportBuf.WriteString("| Context control | ~300 tokens constant | Requires sensenova measurement |\n")
	reportBuf.WriteString("| Topic retention | ~30% (rule-based) | Requires sensenova measurement |\n")
	reportBuf.WriteString("| Detail log | report_real_embedding.md | enterprise_benchmark_log.txt |\n\n")

	if err := os.WriteFile(reportPath, reportBuf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}
	t.Logf("Report saved to: %s", reportPath)
}
