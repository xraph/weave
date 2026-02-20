package assembler

// TokenCounter estimates token counts for text.
type TokenCounter interface {
	// CountTokens returns the estimated number of tokens in the text.
	CountTokens(text string) int
}

// SimpleTokenCounter estimates tokens by dividing character count by 4.
type SimpleTokenCounter struct{}

// CountTokens returns an approximate token count (~4 chars per token).
func (c *SimpleTokenCounter) CountTokens(text string) int {
	return len(text) / 4
}

// BudgetManager tracks token consumption against a budget.
type BudgetManager struct {
	counter   TokenCounter
	maxTokens int
	used      int
}

// NewBudgetManager creates a new budget manager.
func NewBudgetManager(counter TokenCounter, maxTokens int) *BudgetManager {
	return &BudgetManager{counter: counter, maxTokens: maxTokens}
}

// EstimateTokens returns the token count for the given text.
func (b *BudgetManager) EstimateTokens(text string) int {
	return b.counter.CountTokens(text)
}

// CanFit returns true if the given number of tokens fits in the remaining budget.
func (b *BudgetManager) CanFit(tokens int) bool {
	return b.used+tokens <= b.maxTokens
}

// Consume adds tokens to the used count.
func (b *BudgetManager) Consume(tokens int) {
	b.used += tokens
}

// Used returns the total tokens consumed.
func (b *BudgetManager) Used() int {
	return b.used
}

// Remaining returns the tokens still available.
func (b *BudgetManager) Remaining() int {
	return b.maxTokens - b.used
}
