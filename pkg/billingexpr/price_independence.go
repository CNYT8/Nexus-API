package billingexpr

import (
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// IsUsageIndependentPrice conservatively proves that the price of matchedTier
// does not depend on usage. This is a settlement guard, not the display quota
// classifier. Unsupported syntax, ambiguous tier labels and usage-dependent
// multipliers retain the reservation floor. It never changes call() semantics.
//
// Inspect the original AST: optimization can erase p*0 and unreachable branches.
// Tokens in conditions selecting tiers are allowed; tokens in a price (including
// call(p)), or in a multiplier outside a tier, are not. Every possible leaf with
// the recorded label must be independent, so duplicate labels cannot hide a
// token-priced leaf behind a request-priced one.
func IsUsageIndependentPrice(expression, matchedTier string) bool {
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return false
	}
	found, independent := independentPriceLeaf(tree.Node, matchedTier)
	return found && independent
}

func containsTier(node ast.Node) bool {
	return ast.Find(node, func(n ast.Node) bool {
		id, ok := n.(*ast.IdentifierNode)
		return ok && id.Value == "tier"
	}) != nil
}

func independentPriceLeaf(node ast.Node, matchedTier string) (found, independent bool) {
	switch n := node.(type) {
	case *ast.ConditionalNode:
		if !containsTier(n) {
			return matchedTier == "", usageIndependentValue(n)
		}
		// tier() in a condition has trace side effects; do not trust its label.
		if containsTier(n.Cond) {
			return true, false
		}
		left, leftOK := independentPriceLeaf(n.Exp1, matchedTier)
		right, rightOK := independentPriceLeaf(n.Exp2, matchedTier)
		return left || right, (!left || leftOK) && (!right || rightOK)
	case *ast.BinaryNode:
		// Standard request multipliers are outside the conditional pricing tree.
		// Other compositions of tiers are intentionally not inferred from the
		// last tier() trace (e.g. adding two tiers together).
		if n.Operator == "*" {
			if containsTier(n.Left) && usageIndependentValue(n.Right) {
				return independentPriceLeaf(n.Left, matchedTier)
			}
			if containsTier(n.Right) && usageIndependentValue(n.Left) {
				return independentPriceLeaf(n.Right, matchedTier)
			}
		}
	case *ast.CallNode:
		if id, ok := n.Callee.(*ast.IdentifierNode); ok && id.Value == "tier" {
			if len(n.Arguments) != 2 {
				return true, false
			}
			label, ok := n.Arguments[0].(*ast.StringNode)
			if !ok || containsTier(n.Arguments[1]) {
				return true, false
			}
			if label.Value != matchedTier {
				return false, false
			}
			return true, usageIndependentValue(n.Arguments[1])
		}
	}
	if matchedTier != "" || containsTier(node) {
		return true, false
	}
	return true, usageIndependentValue(node)
}

// Whitelist known pure operations. In particular, identifiers, let bindings,
// closures, member access and unknown/future functions fail closed. Conditions
// *inside* a price must also be usage independent.
func usageIndependentValue(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.IntegerNode, *ast.FloatNode, *ast.StringNode, *ast.BoolNode, *ast.NilNode:
		return true
	case *ast.UnaryNode:
		return usageIndependentValue(n.Node)
	case *ast.BinaryNode:
		return usageIndependentValue(n.Left) && usageIndependentValue(n.Right)
	case *ast.ConditionalNode:
		return usageIndependentValue(n.Cond) && usageIndependentValue(n.Exp1) && usageIndependentValue(n.Exp2)
	case *ast.BuiltinNode:
		switch n.Name {
		case "max", "min", "abs", "ceil", "floor":
			for _, arg := range n.Arguments {
				if !usageIndependentValue(arg) {
					return false
				}
			}
			return true
		}
	case *ast.CallNode:
		id, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return false
		}
		switch id.Value {
		case "call", "header", "param", "has", "hour", "minute", "weekday", "month", "day", "max", "min", "abs", "ceil", "floor":
			for _, arg := range n.Arguments {
				if !usageIndependentValue(arg) {
					return false
				}
			}
			return true
		}
	}
	return false
}
