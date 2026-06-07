package setup

import (
	"fmt"
	"math/rand"
)

const RootAgentName = "benchmark-agent"

func RandomUserPrompt(rng *rand.Rand) string {
	verbs := []string{"Analyze", "Review", "Summarize", "Evaluate", "Process"}
	nouns := []string{"system state", "metrics", "workflow", "request batch", "task queue"}
	return fmt.Sprintf("%s the %s for benchmark run %d.", verbs[rng.Intn(len(verbs))], nouns[rng.Intn(len(nouns))], rng.Intn(1_000_000))
}

func systemPrompt(rng *rand.Rand) string {
	topics := []string{"analysis", "planning", "summarization", "debugging", "research"}
	return fmt.Sprintf("You are a benchmark assistant focused on %s. Respond concisely.", topics[rng.Intn(len(topics))])
}

func RootSystemPrompt(treeRng *rand.Rand) string {
	if treeRng == nil {
		treeRng = TreeRNG()
	}
	return systemPrompt(treeRng)
}
