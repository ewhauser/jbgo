package syntax

// FeatureCategory groups variant-gated syntax features into stable families.
type FeatureCategory uint8

const (
	FeatureCategoryUnknown FeatureCategory = iota
	FeatureCategoryArithmetic
	FeatureCategoryArray
	FeatureCategoryBuiltin
	FeatureCategoryCase
	FeatureCategoryConditional
	FeatureCategoryFunction
	FeatureCategoryLoop
	FeatureCategoryParameterExpansion
	FeatureCategoryPattern
	FeatureCategoryRedirection
	FeatureCategorySubstitution
)

func (c FeatureCategory) String() string {
	switch c {
	case FeatureCategoryUnknown:
		return "unknown"
	case FeatureCategoryArithmetic:
		return "arithmetic"
	case FeatureCategoryArray:
		return "array"
	case FeatureCategoryBuiltin:
		return "builtin"
	case FeatureCategoryCase:
		return "case"
	case FeatureCategoryConditional:
		return "conditional"
	case FeatureCategoryFunction:
		return "function"
	case FeatureCategoryLoop:
		return "loop"
	case FeatureCategoryParameterExpansion:
		return "parameter_expansion"
	case FeatureCategoryPattern:
		return "pattern"
	case FeatureCategoryRedirection:
		return "redirection"
	case FeatureCategorySubstitution:
		return "substitution"
	}
	return "unknown"
}

// FeatureID identifies a stable family of variant-gated syntax rejections.
type FeatureID uint16

const (
	FeatureUnknown FeatureID = iota
	FeatureArithmeticUnsignedExpr
	FeatureArithmeticSubscriptFlags
	FeatureArithmeticFloatingPoint
	FeatureSubstitutionReplyVarCmdSubst
	FeatureSubstitutionTempFileCmdSubst
	FeatureSubstitutionProcess
	FeaturePatternExtendedGlob
	FeatureParameterExpansionFlags
	FeatureParameterExpansionWidthPrefix
	FeatureParameterExpansionIndirectPrefix
	FeatureParameterExpansionIsSetPrefix
	FeatureParameterExpansionSearchReplace
	FeatureParameterExpansionSlice
	FeatureParameterExpansionCaseOperator
	FeatureParameterExpansionNameOperator
	FeatureParameterExpansionNested
	FeatureParameterExpansionMatchOperator
	FeatureArraySyntax
	FeatureRedirectionNamedFileDescriptor
	FeatureRedirectionOperator
	FeatureRedirectionHereString
	FeatureRedirectionBeforeCompound
	FeatureFunctionAnonymous
	FeatureFunctionMultiName
	FeatureBuiltinFunctionKeyword
	FeatureBuiltinKeywordLike
	FeatureLoopBraceFor
	FeatureLoopCStyleFor
	FeatureCaseKornForm
	FeatureConditionalRegexTest
	FeatureParameterExpansionGlobSubstPrefix
)

func (id FeatureID) String() string {
	switch id {
	case FeatureUnknown:
		return "unknown"
	case FeatureArithmeticUnsignedExpr:
		return "arithmetic_unsigned_expr"
	case FeatureArithmeticSubscriptFlags:
		return "arithmetic_subscript_flags"
	case FeatureArithmeticFloatingPoint:
		return "arithmetic_floating_point"
	case FeatureSubstitutionReplyVarCmdSubst:
		return "substitution_reply_var_cmd_subst"
	case FeatureSubstitutionTempFileCmdSubst:
		return "substitution_temp_file_cmd_subst"
	case FeatureSubstitutionProcess:
		return "substitution_process"
	case FeaturePatternExtendedGlob:
		return "pattern_extended_glob"
	case FeatureParameterExpansionFlags:
		return "parameter_expansion_flags"
	case FeatureParameterExpansionWidthPrefix:
		return "parameter_expansion_width_prefix"
	case FeatureParameterExpansionIndirectPrefix:
		return "parameter_expansion_indirect_prefix"
	case FeatureParameterExpansionIsSetPrefix:
		return "parameter_expansion_is_set_prefix"
	case FeatureParameterExpansionGlobSubstPrefix:
		return "parameter_expansion_glob_subst_prefix"
	case FeatureParameterExpansionSearchReplace:
		return "parameter_expansion_search_replace"
	case FeatureParameterExpansionSlice:
		return "parameter_expansion_slice"
	case FeatureParameterExpansionCaseOperator:
		return "parameter_expansion_case_operator"
	case FeatureParameterExpansionNameOperator:
		return "parameter_expansion_name_operator"
	case FeatureParameterExpansionNested:
		return "parameter_expansion_nested"
	case FeatureParameterExpansionMatchOperator:
		return "parameter_expansion_match_operator"
	case FeatureArraySyntax:
		return "array_syntax"
	case FeatureRedirectionNamedFileDescriptor:
		return "redirection_named_file_descriptor"
	case FeatureRedirectionOperator:
		return "redirection_operator"
	case FeatureRedirectionHereString:
		return "redirection_here_string"
	case FeatureRedirectionBeforeCompound:
		return "redirection_before_compound"
	case FeatureFunctionAnonymous:
		return "function_anonymous"
	case FeatureFunctionMultiName:
		return "function_multi_name"
	case FeatureBuiltinFunctionKeyword:
		return "builtin_function_keyword"
	case FeatureBuiltinKeywordLike:
		return "builtin_keyword_like"
	case FeatureLoopBraceFor:
		return "loop_brace_for"
	case FeatureLoopCStyleFor:
		return "loop_c_style_for"
	case FeatureCaseKornForm:
		return "case_korn_form"
	case FeatureConditionalRegexTest:
		return "conditional_regex_test"
	}
	return "unknown"
}

func (id FeatureID) Category() FeatureCategory {
	switch id {
	case FeatureArithmeticUnsignedExpr, FeatureArithmeticSubscriptFlags, FeatureArithmeticFloatingPoint:
		return FeatureCategoryArithmetic
	case FeatureArraySyntax:
		return FeatureCategoryArray
	case FeatureBuiltinFunctionKeyword, FeatureBuiltinKeywordLike:
		return FeatureCategoryBuiltin
	case FeatureCaseKornForm:
		return FeatureCategoryCase
	case FeatureConditionalRegexTest:
		return FeatureCategoryConditional
	case FeatureFunctionAnonymous, FeatureFunctionMultiName:
		return FeatureCategoryFunction
	case FeatureLoopBraceFor, FeatureLoopCStyleFor:
		return FeatureCategoryLoop
	case FeatureParameterExpansionFlags,
		FeatureParameterExpansionWidthPrefix,
		FeatureParameterExpansionIndirectPrefix,
		FeatureParameterExpansionIsSetPrefix,
		FeatureParameterExpansionGlobSubstPrefix,
		FeatureParameterExpansionSearchReplace,
		FeatureParameterExpansionSlice,
		FeatureParameterExpansionCaseOperator,
		FeatureParameterExpansionNameOperator,
		FeatureParameterExpansionNested,
		FeatureParameterExpansionMatchOperator:
		return FeatureCategoryParameterExpansion
	case FeaturePatternExtendedGlob:
		return FeatureCategoryPattern
	case FeatureRedirectionNamedFileDescriptor,
		FeatureRedirectionOperator,
		FeatureRedirectionHereString,
		FeatureRedirectionBeforeCompound:
		return FeatureCategoryRedirection
	case FeatureSubstitutionReplyVarCmdSubst,
		FeatureSubstitutionTempFileCmdSubst,
		FeatureSubstitutionProcess:
		return FeatureCategorySubstitution
	default:
		return FeatureCategoryUnknown
	}
}

// Format renders the legacy compatibility text for a variant-gated feature.
func (id FeatureID) Format(detail string) string {
	switch id {
	case FeatureArithmeticUnsignedExpr:
		return "unsigned expressions"
	case FeatureArithmeticSubscriptFlags:
		return "subscript flags"
	case FeatureArithmeticFloatingPoint:
		return "floating point arithmetic"
	case FeatureSubstitutionReplyVarCmdSubst:
		return "`${|stmts;}`"
	case FeatureSubstitutionTempFileCmdSubst:
		return "`${ stmts;}`"
	case FeatureSubstitutionProcess:
		if detail == "" {
			return "process substitutions"
		}
		return detail + " process substitutions"
	case FeaturePatternExtendedGlob:
		return "extended globs"
	case FeatureParameterExpansionFlags:
		return "parameter expansion flags"
	case FeatureParameterExpansionWidthPrefix:
		return "`${%foo}`"
	case FeatureParameterExpansionIndirectPrefix:
		return "`${!foo}`"
	case FeatureParameterExpansionIsSetPrefix:
		return "`${+foo}`"
	case FeatureParameterExpansionGlobSubstPrefix:
		return "`${~foo}`"
	case FeatureParameterExpansionSearchReplace:
		return "search and replace"
	case FeatureParameterExpansionSlice:
		return "slicing"
	case FeatureParameterExpansionCaseOperator:
		if detail == "" {
			return "this expansion operator"
		}
		return "this expansion operator"
	case FeatureParameterExpansionNameOperator:
		if detail == "" {
			return "this expansion operator"
		}
		return "`${!foo" + detail + "}`"
	case FeatureParameterExpansionNested:
		return "nested parameter expansions"
	case FeatureParameterExpansionMatchOperator:
		if detail == "" {
			return "this expansion operator"
		}
		return "${name" + detail + "arg}"
	case FeatureArraySyntax:
		return "arrays"
	case FeatureRedirectionNamedFileDescriptor:
		return "`{varname}` redirects"
	case FeatureRedirectionOperator:
		if detail == "" {
			return "redirects"
		}
		return detail + " redirects"
	case FeatureRedirectionHereString:
		return "herestrings"
	case FeatureRedirectionBeforeCompound:
		return "redirects before compound commands"
	case FeatureFunctionAnonymous:
		return "anonymous functions"
	case FeatureFunctionMultiName:
		return "multi-name functions"
	case FeatureBuiltinFunctionKeyword:
		return `the "function" builtin`
	case FeatureBuiltinKeywordLike:
		if detail == "" {
			return "the builtin"
		}
		return "the " + detail + " builtin"
	case FeatureLoopBraceFor:
		return "for loops with braces"
	case FeatureLoopCStyleFor:
		return "c-style fors"
	case FeatureCaseKornForm:
		return "`case i {`"
	case FeatureConditionalRegexTest:
		return "regex tests"
	case FeatureUnknown:
		fallthrough
	default:
		if detail != "" {
			return detail
		}
		return "unknown feature"
	}
}
