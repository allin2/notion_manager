package proxy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// FailWhy classifies why an upstream (Notion) request failed, borrowed from
// magpie's routing design: each class gets its own rest duration and
// recovery policy instead of a single "exhausted" bit.
type FailWhy string

const (
	// FailQuota: the account's AI usage / credits are used up. Recovery is
	// API-confirmed (applyQuotaInfo clears the legacy exhausted flag) or
	// time-based backoff while the live check keeps confirming exhaustion.
	FailQuota FailWhy = "quota"
	// FailRate: a short-term rate limit (429 without quota wording).
	FailRate FailWhy = "rate"
	// FailAuth: the token_v2 stopped working (expired / signed out). Needs a
	// re-import; the dashboard can lift the rest by hand.
	FailAuth FailWhy = "auth"
	// FailRestricted: the account itself was restricted by the vendor.
	FailRestricted FailWhy = "restricted"
	// FailModel: the account is not served this model (plan / gray-list
	// gating). Only this (account, model) pair rests; the account keeps
	// serving its other models.
	FailModel FailWhy = "model"
	// FailOther: anything else (5xx, malformed response, network).
	FailOther FailWhy = "other"
)

// Rest records why a candidate (account, or account+model pair) sits out
// and until when. The dashboard surfaces these fields so the operator can
// see why an account is disabled and when it is expected to come back.
type Rest struct {
	Why      FailWhy   `json:"why"`
	Status   int       `json:"status,omitempty"`
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	Failures int       `json:"failures"` // consecutive failures for this rest key
	Model    string    `json:"model,omitempty"`
	Detail   string    `json:"detail,omitempty"` // short error excerpt for the dashboard
}

// key returns the rest-map key: "" for the whole account, or the model
// name for a model-scoped rest.
func (r *Rest) key() string { return r.Model }

// Rest duration policy per failure class. Backoff classes double their
// base per consecutive failure, capped.
type restPolicy struct {
	base        time.Duration
	cap         time.Duration
	backoff     bool // multiply base by 2^(failures-1), capped
	modelScoped bool // rest only the (account, model) pair
}

var restPolicies = map[FailWhy]restPolicy{
	FailQuota:      {base: 30 * time.Minute, cap: 2 * time.Hour, backoff: true},
	FailRate:       {base: 1 * time.Minute, cap: 30 * time.Minute, backoff: true},
	FailAuth:       {base: 6 * time.Hour, cap: 6 * time.Hour},
	FailRestricted: {base: 2 * time.Hour, cap: 2 * time.Hour},
	FailModel:      {base: 2 * time.Hour, cap: 2 * time.Hour, modelScoped: true},
	FailOther:      {base: 1 * time.Minute, cap: 10 * time.Minute, backoff: true},
}

// NotionFailure is a classified upstream failure. Quota-class failures wrap
// ErrQuotaExhausted so every existing errors.Is(ErrQuotaExhausted) branch
// keeps working.
type NotionFailure struct {
	Why    FailWhy
	Status int
	Body   string
}

func (e *NotionFailure) Error() string {
	return fmt.Sprintf("notion API error %d: %s", e.Status, e.Body)
}

func (e *NotionFailure) Unwrap() error {
	if e.Why == FailQuota {
		return ErrQuotaExhausted
	}
	return nil
}

// Error word lists. Notion's error bodies are mostly English; common
// Chinese phrasings are included for safety.
var (
	modelWords = regexp.MustCompile(`(?i)model (is )?(not|isn't|un)?(available|allowed|supported|enabled|accessible)|not available (on|for|to) (your|this) (plan|account|model)|does ?n[o']t have access to (this|the|your) model|unsupported model|model not (in|on) (your|the) plan|模型不可用|不支持(该|此)?模型|模型(暂)?未(开放|开通)|无(权|法)使用(该|此)?模型`)
	authWords  = regexp.MustCompile(`(?i)unauthorized|invalid.?token|token.?expired|not (logged|signed) in|authentication|sign.?in required|please log?in|身份验证|登录(已过期|失效)|未授权|需要登录`)
	restrWords = regexp.MustCompile(`(?i)restricted|blocked|suspended|banned|abusive|violation|违反|限制使用|封禁|异常行为|违规`)
	quotaWords = regexp.MustCompile(`(?i)usage.?limit|limit.?reached|limit has been (reached|exceeded)|quota|exceeded your|out of (credits?|usage)|credit.{0,12}(used|exhausted|run out)|upgrade (your|to a) (plan|subscription)|billing|额度|用量|上限|已用完|已达到|配额`)
	rateWords  = regexp.MustCompile(`(?i)rate.?limit|too many requests|throttl|slow down|try again (later|in)|请求(过于|太)频繁|限流|稍后再试`)
)

// wordsOf decodes a JSON body's strings so escaped non-ASCII (\uXXXX) error
// text still matches the word lists.
func wordsOf(body []byte) string {
	if !strings.Contains(string(body), `\u`) {
		return string(body)
	}
	var v interface{}
	if json.Unmarshal(body, &v) != nil {
		return string(body)
	}
	var b strings.Builder
	b.Write(body)
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch t := x.(type) {
		case string:
			b.WriteByte('\n')
			b.WriteString(t)
		case map[string]interface{}:
			for _, y := range t {
				walk(y)
			}
		case []interface{}:
			for _, y := range t {
				walk(y)
			}
		}
	}
	walk(v)
	return b.String()
}

// classifyNotionFailure maps an upstream HTTP failure to a FailWhy.
// Order matters: model gating is checked before quota because plan-gating
// messages often contain quota-adjacent wording, and a 429 that says the
// allowance itself is spent is quota, not a rate limit.
func classifyNotionFailure(status int, body []byte) *NotionFailure {
	words := wordsOf(body)
	nf := &NotionFailure{Status: status, Body: excerptBody(body)}
	switch {
	case status == 401:
		nf.Why = FailAuth
	case status == 403 && restrWords.MatchString(words):
		nf.Why = FailRestricted
	case status == 403:
		nf.Why = FailAuth
	case modelWords.MatchString(words):
		nf.Why = FailModel
	case status == 429 && quotaWords.MatchString(words):
		nf.Why = FailQuota
	case status == 429:
		nf.Why = FailRate
	case status == 402 || quotaWords.MatchString(words):
		nf.Why = FailQuota
	default:
		nf.Why = FailOther
	}
	// A plain 403/401 whose body names a model it refused is model gating
	// on an authenticated request, not a dead token.
	if (nf.Why == FailAuth || nf.Why == FailRestricted) && modelWords.MatchString(words) {
		nf.Why = FailModel
	}
	return nf
}

// excerptBody trims an error body for logs / the dashboard.
func excerptBody(body []byte) string {
	s := strings.TrimSpace(wordsOf(body))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
