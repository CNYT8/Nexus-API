package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

var taskUsageName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)
var taskUsagePath = regexp.MustCompile(`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+){0,7}$`)

type taskUsageExpressionGuard struct{ err error }

func (g *taskUsageExpressionGuard) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode, *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode, *ast.StringNode, *ast.ConditionalNode:
	case *ast.UnaryNode:
		if n.Operator != "+" && n.Operator != "-" && n.Operator != "!" && n.Operator != "not" {
			g.err = errors.New("unsupported task usage operator")
		}
	case *ast.BinaryNode:
		switch n.Operator {
		case "+", "-", "*", "/", "%", "<", "<=", ">", ">=", "==", "!=", "&&", "||", "and", "or":
		default:
			g.err = errors.New("unsupported task usage operator")
		}
	default:
		g.err = errors.New("task usage expression accepts scalar arithmetic and conditions only")
	}
}

func compileTaskUsage(snapshot *model.TaskUsageSnapshot) (*vm.Program, error) {
	if snapshot == nil || len(snapshot.Schema) == 0 || len(snapshot.Schema) > 32 || len(snapshot.Expression) == 0 || len(snapshot.Expression) > 4096 {
		return nil, errors.New("invalid task usage schema or expression size")
	}
	env := make(map[string]any, len(snapshot.Schema))
	for name, field := range snapshot.Schema {
		if !taskUsageName.MatchString(name) || len(field.Path) > 256 || !taskUsagePath.MatchString(field.Path) {
			return nil, fmt.Errorf("invalid task usage field %s", name)
		}
		value, err := taskUsageDefault(field)
		if err != nil {
			return nil, fmt.Errorf("invalid task usage field %s: %w", name, err)
		}
		env[name] = value
	}
	guard := &taskUsageExpressionGuard{}
	program, err := expr.Compile(snapshot.Expression, expr.Env(env), expr.AsFloat64(), expr.DisableAllBuiltins(), expr.MaxNodes(256), expr.Optimize(false), expr.Patch(guard))
	if guard.err != nil {
		return nil, guard.err
	}
	return program, err
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func ValidateTaskPlugin(plugin *model.TaskPlugin) error {
	if plugin == nil || len(plugin.Name) == 0 || len(plugin.Name) > 96 || strings.TrimSpace(plugin.Name) != plugin.Name || len(plugin.Version) == 0 || len(plugin.Version) > 64 || strings.TrimSpace(plugin.Version) != plugin.Version || len(plugin.Schema) > 16384 {
		return errors.New("invalid task plugin name, version or schema size")
	}
	if !json.Valid([]byte(plugin.Schema)) {
		return errors.New("invalid task usage schema JSON")
	}
	var schema map[string]model.TaskUsageField
	decoder := json.NewDecoder(strings.NewReader(plugin.Schema))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil {
		return err
	}
	_, err := compileTaskUsage(&model.TaskUsageSnapshot{Schema: schema, Expression: plugin.Expression})
	return err
}

// FreezeTaskUsageBilling runs before dispatch, after member/user group pricing.
// Missing binding keeps dynamic pricing. A bad configured revision fails locally.
func FreezeTaskUsageBilling(modelName string, groupRatio float64, ratios map[string]float64) (*model.TaskUsageSnapshot, error) {
	id := operation_setting.GetTaskUsagePluginID(modelName)
	if id <= 0 {
		return nil, nil
	}
	plugin, err := model.GetTaskPlugin(id)
	if err != nil {
		return nil, err
	}
	return NewTaskUsageSnapshot(plugin, groupRatio, ratios)
}

// NewTaskUsageSnapshot is shared by production submit and dry-run preview.
// Schema and multiplier maps are copied so later edits cannot affect the task.
func NewTaskUsageSnapshot(plugin *model.TaskPlugin, groupRatio float64, ratios map[string]float64) (*model.TaskUsageSnapshot, error) {
	if err := ValidateTaskPlugin(plugin); err != nil {
		return nil, err
	}
	if len(ratios) > 64 {
		return nil, errors.New("too many task usage multipliers")
	}
	snapshot := &model.TaskUsageSnapshot{PluginID: plugin.ID, Name: plugin.Name, Version: plugin.Version, Expression: plugin.Expression, GroupRatio: groupRatio, QuotaPerUnit: common.QuotaPerUnit, OtherRatios: make(map[string]float64, len(ratios))}
	if plugin.RetainResult != nil {
		retain := *plugin.RetainResult
		snapshot.RetainResult = &retain
	}
	if err := json.Unmarshal([]byte(plugin.Schema), &snapshot.Schema); err != nil {
		return nil, err
	}
	if !finiteNonnegative(groupRatio) || !finiteNonnegative(snapshot.QuotaPerUnit) || snapshot.QuotaPerUnit == 0 {
		return nil, errors.New("invalid task usage ratio")
	}
	for name, ratio := range ratios {
		if name == "" || len(name) > 128 || !finiteNonnegative(ratio) {
			return nil, fmt.Errorf("invalid task usage multiplier %s", name)
		}
		snapshot.OtherRatios[name] = ratio
	}
	return snapshot, nil
}

func EvaluateTaskUsage(snapshot *model.TaskUsageSnapshot, response []byte) (int, error) {
	program, err := compileTaskUsage(snapshot)
	if err != nil {
		return 0, err
	}
	if len(response) == 0 || len(response) > 4*1024*1024 || !json.Valid(response) {
		return 0, errors.New("task usage response exceeds size limit or is invalid JSON")
	}
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return 0, err
	}
	env := make(map[string]any, len(snapshot.Schema))
	for name, field := range snapshot.Schema {
		var value any = body
		found := true
		for _, key := range strings.Split(field.Path, ".") {
			object, ok := value.(map[string]any)
			if !ok {
				found = false
				break
			}
			value, found = object[key]
			if !found {
				break
			}
		}
		if !found || value == nil {
			if field.Required {
				return 0, fmt.Errorf("missing task usage field %s", name)
			}
			value, err = taskUsageDefault(field)
		}
		if err != nil {
			return 0, err
		}
		env[name], err = validateTaskUsageValue(field, value)
		if err != nil {
			return 0, fmt.Errorf("task usage field %s: %w", name, err)
		}
	}
	machine := vm.VM{MemoryBudget: 4096}
	result, err := machine.Run(program, env)
	if err != nil {
		return 0, err
	}
	cost, ok := result.(float64)
	if !ok || !finiteNonnegative(cost) || !finiteNonnegative(snapshot.GroupRatio) || !finiteNonnegative(snapshot.QuotaPerUnit) || snapshot.QuotaPerUnit == 0 {
		return 0, errors.New("task usage expression returned invalid cost")
	}
	cost *= snapshot.GroupRatio * snapshot.QuotaPerUnit
	names := make([]string, 0, len(snapshot.OtherRatios))
	for name := range snapshot.OtherRatios {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ratio := snapshot.OtherRatios[name]
		if !finiteNonnegative(ratio) {
			return 0, errors.New("invalid frozen task multiplier")
		}
		cost *= ratio
	}
	return common.QuotaRoundStrict(cost)
}

// An invalid/missing provider usage keeps the submit quote. It never silently
// produces a zero refund, changes the frozen policy, or blocks task completion.
func TaskUsageBillingTarget(task *model.Task) (int, string, bool) {
	if task == nil || task.PrivateData.BillingContext == nil || task.PrivateData.BillingContext.UsageSnapshot == nil {
		return 0, "", false
	}
	// A discarded immediate result was evaluated before insertion. Reuse its
	// durable target during recovery; missing raw data must not reprice it.
	if task.PrivateData.ResultDiscarded {
		return task.BillingQuota, "提交时已冻结用量结算", true
	}
	quota, err := EvaluateTaskUsage(task.PrivateData.BillingContext.UsageSnapshot, task.Data)
	if err != nil {
		return task.Quota, "用量计费数据无效，保留预扣额度", true
	}
	return quota, "任务用量表达式结算", true
}
