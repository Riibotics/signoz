package rules

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/model"
	v3 "github.com/SigNoz/signoz/pkg/query-service/model/v3"
	"github.com/SigNoz/signoz/pkg/query-service/utils/labels"
	"github.com/pkg/errors"
)

// this file contains common structs and methods used by
// rule engine

const (
	// how long before re-sending the alert
	ResolvedRetention = 15 * time.Minute

	TestAlertPostFix = "_TEST_ALERT"
)

type RuleType string

const (
	RuleTypeThreshold = "threshold_rule"
	RuleTypeProm      = "promql_rule"
	RuleTypeAnomaly   = "anomaly_rule"
)

type RuleHealth string

const (
	HealthUnknown RuleHealth = "unknown"
	HealthGood    RuleHealth = "ok"
	HealthBad     RuleHealth = "err"
)

type Alert struct {
	State model.AlertState

	Labels      labels.BaseLabels
	Annotations labels.BaseLabels

	QueryResultLables labels.BaseLabels

	GeneratorURL string

	// list of preferred receivers, e.g. slack
	Receivers []string

	Value      float64
	ActiveAt   time.Time
	FiredAt    time.Time
	ResolvedAt time.Time
	LastSentAt time.Time
	ValidUntil time.Time

	Missing bool
}

func (a *Alert) needsSending(ts time.Time, resendDelay time.Duration) bool {
	if a.State == model.StatePending {
		return false
	}

	// if an alert has been resolved since the last send, resend it
	if a.ResolvedAt.After(a.LastSentAt) {
		return true
	}

	return a.LastSentAt.Add(resendDelay).Before(ts)
}

type NamedAlert struct {
	Name string
	*Alert
}

type CompareOp string

const (
	CompareOpNone      CompareOp = "0"
	ValueIsAbove       CompareOp = "1"
	ValueIsBelow       CompareOp = "2"
	ValueIsEq          CompareOp = "3"
	ValueIsNotEq       CompareOp = "4"
	ValueAboveOrEq     CompareOp = "5"
	ValueBelowOrEq     CompareOp = "6"
	ValueOutsideBounds CompareOp = "7"
)

var (
	supportedCompareOps = []CompareOp{CompareOpNone, ValueIsAbove, ValueIsBelow, ValueIsEq, ValueIsNotEq, ValueAboveOrEq, ValueBelowOrEq, ValueOutsideBounds}
)

func (co CompareOp) String() string {
	switch co {
	case CompareOpNone:
		return "None: Enum value 0"
	case ValueIsAbove:
		return "ValueIsAbove: Enum value 1"
	case ValueIsBelow:
		return "ValueIsBelow: Enum value 2"
	case ValueIsEq:
		return "ValueIsEq: Enum value 3"
	case ValueIsNotEq:
		return "ValueIsNotEq: Enum value 4"
	case ValueAboveOrEq:
		return "ValueAboveOrEq: Enum value 5"
	case ValueBelowOrEq:
		return "ValueBelowOrEq: Enum value 6"
	case ValueOutsideBounds:
		return "ValueOutsideBounds: Enum value 7"
	}
	return "Unknown: Enum value"
}

func (co CompareOp) Validate() error {
	var msg string
	for _, op := range supportedCompareOps {
		msg += op.String() + ", "
	}
	switch co {
	case ValueIsAbove, ValueIsBelow, ValueIsEq, ValueIsNotEq, ValueAboveOrEq, ValueBelowOrEq, ValueOutsideBounds:
		return nil
	}
	return fmt.Errorf("invalid compare op: %s supported ops are %s", co, msg)
}

type MatchType string

const (
	MatchTypeNone MatchType = "0"
	AtleastOnce   MatchType = "1"
	AllTheTimes   MatchType = "2"
	OnAverage     MatchType = "3"
	InTotal       MatchType = "4"
	Last          MatchType = "5"
)

var (
	supportedMatchTypes = []MatchType{MatchTypeNone, AtleastOnce, AllTheTimes, OnAverage, InTotal, Last}
)

func (mt MatchType) String() string {
	switch mt {
	case MatchTypeNone:
		return "None: Enum value 0"
	case AtleastOnce:
		return "AtleastOnce: Enum value 1"
	case AllTheTimes:
		return "AllTheTimes: Enum value 2"
	case OnAverage:
		return "OnAverage: Enum value 3"
	case InTotal:
		return "InTotal: Enum value 4"
	case Last:
		return "Last: Enum value 5"
	}
	return "Unknown: Enum value"
}

func (mt MatchType) Validate() error {
	var msg string
	for _, op := range supportedMatchTypes {
		msg += op.String() + ", "
	}
	switch mt {
	case MatchTypeNone, AtleastOnce, AllTheTimes, OnAverage, InTotal, Last:
		return nil
	}
	return fmt.Errorf("invalid match type: %s supported ops are %s", mt, msg)
}

type RuleCondition struct {
	CompositeQuery    *v3.CompositeQuery `json:"compositeQuery,omitempty" yaml:"compositeQuery,omitempty"`
	CompareOp         CompareOp          `yaml:"op,omitempty" json:"op,omitempty"`
	Target            *float64           `yaml:"target,omitempty" json:"target,omitempty"`
	AlertOnAbsent     bool               `yaml:"alertOnAbsent,omitempty" json:"alertOnAbsent,omitempty"`
	AbsentFor         uint64             `yaml:"absentFor,omitempty" json:"absentFor,omitempty"`
	MatchType         MatchType          `json:"matchType,omitempty"`
	TargetUnit        string             `json:"targetUnit,omitempty"`
	Algorithm         string             `json:"algorithm,omitempty"`
	Seasonality       string             `json:"seasonality,omitempty"`
	SelectedQuery     string             `json:"selectedQueryName,omitempty"`
	RequireMinPoints  bool               `yaml:"requireMinPoints,omitempty" json:"requireMinPoints,omitempty"`
	RequiredNumPoints int                `yaml:"requiredNumPoints,omitempty" json:"requiredNumPoints,omitempty"`
}

func (rc *RuleCondition) GetSelectedQueryName() string {
	if rc != nil {
		if rc.SelectedQuery != "" {
			return rc.SelectedQuery
		}

		queryNames := map[string]struct{}{}

		if rc.CompositeQuery != nil {
			if rc.QueryType() == v3.QueryTypeBuilder {
				for name := range rc.CompositeQuery.BuilderQueries {
					queryNames[name] = struct{}{}
				}
			} else if rc.QueryType() == v3.QueryTypeClickHouseSQL {
				for name := range rc.CompositeQuery.ClickHouseQueries {
					queryNames[name] = struct{}{}
				}
			}
		}

		// The following logic exists for backward compatibility
		// If there is no selected query, then
		// - check if F1 is present, if yes, return F1
		// - else return the query with max ascii value
		// this logic is not really correct. we should be considering
		// whether the query is enabled or not. but this is a temporary
		// fix to support backward compatibility
		if _, ok := queryNames["F1"]; ok {
			return "F1"
		}
		keys := make([]string, 0, len(queryNames))
		for k := range queryNames {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys[len(keys)-1]
	}
	// This should never happen
	return ""
}

func (rc *RuleCondition) Validate() error {

	if rc.CompositeQuery == nil {
		return ErrCompositeQueryRequired
	}

	if rc.QueryType() == v3.QueryTypeBuilder {
		if rc.Target == nil {
			return ErrTargetRequired
		}
		if rc.CompareOp == "" {
			return ErrCompareOpRequired
		}
		if err := rc.CompareOp.Validate(); err != nil {
			return err
		}
		if rc.MatchType == "" {
			return ErrMatchTypeRequired
		}
		if err := rc.MatchType.Validate(); err != nil {
			return err
		}
	}

	if err := rc.CompositeQuery.Validate(); err != nil {
		return err
	}
	return nil
}

// QueryType is a short hand method to get query type
func (rc *RuleCondition) QueryType() v3.QueryType {
	if rc.CompositeQuery != nil {
		return rc.CompositeQuery.QueryType
	}
	return v3.QueryTypeUnknown
}

// String is useful in printing rule condition in logs
func (rc *RuleCondition) String() string {
	if rc == nil {
		return ""
	}
	data, _ := json.Marshal(*rc)
	return string(data)
}

type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		*d = Duration(time.Duration(value))
		return nil
	case string:
		tmp, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(tmp)

		return nil
	default:
		return errors.New("invalid duration")
	}
}

// prepareRuleGeneratorURL creates an appropriate url
// for the rule. the URL is sent in slack messages as well as
// to other systems and allows backtracking to the rule definition
// from the third party systems.
func prepareRuleGeneratorURL(ruleId string, source string) string {
	if source == "" {
		return source
	}

	// check if source is a valid url
	parsedSource, err := url.Parse(source)
	if err != nil {
		return ""
	}
	// since we capture window.location when a new rule is created
	// we end up with rulesource host:port/alerts/new. in this case
	// we want to replace new with rule id parameter

	hasNew := strings.LastIndex(source, "new")
	if hasNew > -1 {
		ruleURL := fmt.Sprintf("%sedit?ruleId=%s", source[0:hasNew], ruleId)
		return ruleURL
	}

	// The source contains the encoded query, start and end time
	// and other parameters. We don't want to include them in the generator URL
	// mainly to keep the URL short and lower the alert body contents
	// The generator URL with /alerts/edit?ruleId= is enough
	if parsedSource.Port() != "" {
		return fmt.Sprintf("%s://%s:%s/alerts/edit?ruleId=%s", parsedSource.Scheme, parsedSource.Hostname(), parsedSource.Port(), ruleId)
	}
	return fmt.Sprintf("%s://%s/alerts/edit?ruleId=%s", parsedSource.Scheme, parsedSource.Hostname(), ruleId)
}
