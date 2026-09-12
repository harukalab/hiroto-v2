package tools

import (
	"context"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/hirotomasato/hiroto/internal/llm"
)

// NewRuleEngine creates an initialized RuleEngine.
func NewRuleEngine() *RuleEngine {
	return &RuleEngine{
		History: make(map[string]bool),
	}
}

// AddRule adds a rule, deduplicating by pattern. Returns the rule ID on success.
func (e *RuleEngine) AddRule(rule StreamRule) (string, error) {
	_, err := regexp.Compile(rule.Pattern)
	if err != nil {
		return "", err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Dedup by pattern — replace existing rule.
	for i, r := range e.Rules {
		if r.Pattern == rule.Pattern {
			rule.ID = r.ID
			e.Rules[i] = rule
			return rule.ID, nil
		}
	}

	rule.ID = rule.Pattern
	rule.CreatedAt = time.Now()
	e.Rules = append(e.Rules, rule)
	return rule.ID, nil
}

// RemoveRule removes a rule by its ID.
func (e *RuleEngine) RemoveRule(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i, r := range e.Rules {
		if r.ID == id {
			e.Rules = append(e.Rules[:i], e.Rules[i+1:]...)
			delete(e.History, id)
			return
		}
	}
}

// Match checks if the token matches any rule's pattern.
// Returns the matching rule text if a rule matched, empty string otherwise.
func (e *RuleEngine) Match(token string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, rule := range e.Rules {
		compiled, err := regexp.Compile(rule.Pattern)
		if err == nil && compiled.MatchString(token) {
			return rule.Rule
		}
	}
	return ""
}

// ApplyAndRetry runs an LLM call, checking each token against rules.
// On match, it injects the rule text as a system reminder and retries.
func (e *RuleEngine) ApplyAndRetry(ctx context.Context, llmClient RuleLLM, systemPrompt, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Build initial messages
	messages := []llm.Message{}
	if systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: systemPrompt,
		})
	}
	if prompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleUser,
			Content: prompt,
		})
	}

	var accumulated string
	var retries int64
	const maxRetries = 10
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for {
		if atomic.LoadInt64(&retries) > maxRetries {
			break
		}

		if err := ctx.Err(); err != nil {
			return accumulated, err
		}


		// Build message list: original messages + rule reminders
		callMessages := make([]llm.Message, len(messages))
		copy(callMessages, messages)

		assistant, err := llmClient.Chat(ctx, callMessages, nil)
		if err != nil {
			return accumulated, err
		}

		content, _ := assistant.Content.(string)
		if content == "" {
			break
		}

		accumulated += content

		// Check for rule matches
		matchedRule := e.Match(content)
		if matchedRule != "" {
			id := atomic.AddInt64(&retries, 1)
			if id <= maxRetries {
				// Add a rule reminder as a system message
				reminder := llm.Message{
					Role:    llm.RoleSystem,
					Content: "Per stream rule: " + matchedRule,
				}
				messages = append(messages, reminder)
				continue
			}
		}

		// No more matches — we're done
		break
	}

	return accumulated, nil
}

// ListRules returns all active rules.
func (e *RuleEngine) ListRules() []StreamRule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]StreamRule, len(e.Rules))
	copy(out, e.Rules)
	return out
}
