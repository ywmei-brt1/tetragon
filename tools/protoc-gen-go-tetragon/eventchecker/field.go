// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

package eventchecker

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cilium/tetragon/tools/protoc-gen-go-tetragon/common"
	"github.com/cilium/tetragon/tools/protoc-gen-go-tetragon/imports"
	"github.com/iancoleman/strcase"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	checkerVarName = "checker"
	eventVarName   = "event"
)

type Field struct {
	*protogen.Field
	IsInnerField bool
}

func (field *Field) generateWith(g *protogen.GeneratedFile, msg *CheckedMessage) error {
	typeName, err := field.typeName(g)
	if err != nil {
		return err
	}

	g.P(`// With` + field.GoName + ` adds a ` + field.GoName + ` check to the ` + msg.checkerName(g))
	if field.isPrimitive() && (!field.isList() && !field.isMap()) {
		g.P(`func (checker *` + msg.checkerName(g) + `) With` + field.GoName + `(check ` + typeName + `) *` + msg.checkerName(g) + `{
            checker.` + field.GoName + ` = &check`)
	} else if field.isList() {
		g.P(`func (checker *` + msg.checkerName(g) + `) With` + field.GoName + `(check *` + field.listCheckerName(g) + `) *` + msg.checkerName(g) + `{
            checker.` + field.GoName + ` = check`)
	} else if field.isMap() {
		g.P(`func (checker *` + msg.checkerName(g) + `) With` + field.GoName + `(check ` + typeName + `) *` + msg.checkerName(g) + `{
            checker.` + field.GoName + ` = check`)
	} else if field.isEnum() {
		enumIdent := common.TetragonApiIdent(g, field.Enum.GoIdent.GoName)
		g.P(`func (checker *` + msg.checkerName(g) + `) With` + field.GoName + `(check ` + enumIdent + `) *` + msg.checkerName(g) + `{
            wrappedCheck := ` + typeName + `(check)
            checker.` + field.GoName + ` = &wrappedCheck`)
	} else {
		g.P(`func (checker *` + msg.checkerName(g) + `) With` + field.GoName + `(check *` + typeName + `) *` + msg.checkerName(g) + `{
            checker.` + field.GoName + ` = check`)
	}
	g.P(`return checker
    }`)

	return nil
}

func (field *Field) generateFrom(g *protogen.GeneratedFile, msg *CheckedMessage) error {
	from, err := field.getFrom(g, msg)
	if err != nil {
		return err
	}

	g.P(from)

	return nil
}

func (field *Field) getFrom(g *protogen.GeneratedFile, msg *CheckedMessage) (string, error) {
	checkerVar := fmt.Sprintf("%s.%s", checkerVarName, field.GoName)
	eventVar := fmt.Sprintf("%s.%s", eventVarName, field.GoName)

	from, err := doGetFieldFrom(field, g, true, true, msg.checkerName(g), checkerVar, eventVar)
	if err != nil {
		return "", err
	}

	return from, nil
}

func doGetFieldFrom(field *Field, g *protogen.GeneratedFile, handleList, handleOneof bool,
	checkerName, checkerVar, eventVar string) (string, error) {
	kind := field.Desc.Kind()

	doPrimitiveFrom := func() string {
		if !field.IsInnerField && (field.isList() || field.isMap()) {
			return checkerVar + ` = ` + eventVar
		}
		return `{
        val := ` + eventVar + `
        ` + checkerVar + ` = &val
        }`
	}

	doStringFrom := func() string {
		fullSmatcher := common.StringMatcherIdent(g, "Full")
		return checkerVar + ` = ` + fullSmatcher + `(` + eventVar + `)`
	}

	doBytesFrom := func() string {
		fullBmatcher := common.BytesMatcherIdent(g, "Full")
		return checkerVar + ` = ` + fullBmatcher + `(` + eventVar + `)`
	}

	doLabelsFrom := func() string {
		return "// TODO from labels"
	}

	doOneofFrom := func(oneof *protogen.Oneof) (string, error) {
		innerFrom, err := doGetFieldFrom(field, g, handleList, false, checkerName, checkerVar, "event."+field.GoName)
		if err != nil {
			return "", err
		}

		innerType := common.TetragonApiIdent(g, field.GoIdent.GoName)

		return `switch event := ` + fmt.Sprintf("%s.%s", eventVarName, oneof.GoName) + `.(type) {
            case * ` + innerType + `:
                ` + innerFrom + `
            }`, nil
	}

	doListFrom := func() (string, error) {
		matchKind := common.ListMatcherIdent(g, "Ordered")
		typeName, err := field.typeName(g)
		if err != nil {
			return "", err
		}

		innerFrom, err := doGetFieldFrom(field, g, false, handleOneof, checkerName, "convertedCheck", "check")
		if err != nil {
			return "", err
		}

		innerType, err := field.typeName(g)
		if err != nil {
			return "", err
		}
		innerType = strings.TrimPrefix(innerType, "[]")

		return `{
            var checks ` + typeName + `
            for _, check := range ` + eventVar + ` {
                var convertedCheck ` + innerType + `
                ` + innerFrom + `
                checks = append(checks, convertedCheck)
            }
            lm := ` + field.newListCheckerName(g) + `().WithOperator(` + matchKind + `).
                WithValues(checks...)
            ` + checkerVar + ` = lm
        }`, nil
	}

	doWrapperFrom := func() string {
		return `if ` + eventVar + ` != nil {
            val := ` + eventVar + `.Value
            ` + checkerVar + ` = &val
        }`
	}

	doDurationFrom := func() string {
		return `// NB: We don't want to match durations for now
        ` + checkerVar + ` = nil`
	}

	doTimestampFrom := func() string {
		return `// NB: We don't want to match timestamps for now
        ` + checkerVar + ` = nil`
	}

	doCheckerFrom := func() string {
		newCall := fmt.Sprintf("New%sChecker()", field.Message.GoIdent.GoName)
		typeImportPath := string(field.Message.GoIdent.GoImportPath)
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
			newCall = g.QualifiedGoIdent(protogen.GoIdent{
				GoName:       newCall,
				GoImportPath: protogen.GoImportPath(importPath),
			})
		}

		return `if ` + eventVar + ` != nil {
        ` + checkerVar + `= ` + newCall + `.From` +
			field.Message.GoIdent.GoName + `(` + eventVar + `)
        }`
	}

	doEnumFrom := func() string {
		typeImportPath := string(field.Enum.GoIdent.GoImportPath)
		newCall := fmt.Sprintf("New%sChecker", field.Enum.GoIdent.GoName)
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
			newCall = g.QualifiedGoIdent(protogen.GoIdent{
				GoName:       newCall,
				GoImportPath: protogen.GoImportPath(importPath),
			})
		}
		return checkerVar + `= ` + newCall + `(` + eventVar + `)`
	}

	// Pod.Labels is a special case
	if field.GoIdent.GoName == "Pod_Labels" {
		return doLabelsFrom(), nil
	}

	if handleOneof && field.Oneof != nil {
		return doOneofFrom(field.Oneof)
	}

	if handleList && field.Desc.IsList() {
		return doListFrom()
	}

	if handleList && field.Desc.IsMap() {
		return "// TODO: implement fromMap", nil
	}

	switch kind {
	case protoreflect.BoolKind,
		protoreflect.Int32Kind,
		protoreflect.Sint32Kind,
		protoreflect.Uint32Kind,
		protoreflect.Int64Kind,
		protoreflect.Sint64Kind,
		protoreflect.Uint64Kind,
		protoreflect.FloatKind,
		protoreflect.DoubleKind:
		return doPrimitiveFrom(), nil

	case protoreflect.BytesKind:
		return doBytesFrom(), nil

	case protoreflect.StringKind:
		return doStringFrom(), nil

	case protoreflect.MessageKind:
		switch field.Message.GoIdent.GoImportPath {
		case imports.WrappersPath:
			return doWrapperFrom(), nil
		case imports.TimestampPath:
			return doTimestampFrom(), nil
		case imports.DurationPath:
			return doDurationFrom(), nil
		}
		return doCheckerFrom(), nil

	case protoreflect.EnumKind:
		return doEnumFrom(), nil

	default:
		return "", fmt.Errorf("unhandled field type %s (please edit doGetFieldFrom in field.go)", kind)
	}
}

func (field *Field) generateFieldCheck(g *protogen.GeneratedFile, msg *CheckedMessage) error {
	checkerVar := fmt.Sprintf("%s.%s", checkerVarName, field.GoName)
	eventVar := fmt.Sprintf("%s.%s", eventVarName, field.GoName)

	check, err := field.getFieldCheck(g, msg.checkerName(g), checkerVar, eventVar)
	if err != nil {
		return err
	}

	if !field.isMap() {
		check = `if ` + checkerVar + ` != nil {
            ` + check + `
        }`
	}

	g.P(check)

	return nil
}

func (field *Field) getFieldCheck(g *protogen.GeneratedFile, checkerName, checkerVar, eventVar string) (string, error) {
	if field.Oneof != nil {
		return checkForOneof(g, field, checkerName, checkerVar)
	}

	if field.isList() {
		return `if err := ` + checkerVar + `.Check(` + eventVar + `); err != nil {
		return ` + common.FmtErrorf(g, field.GoName+" check failed: %w", "err") + `
		}`, nil
	}

	if field.isMap() {
		// Wrap the check in its own scope so we can redeclare variables
		check, err := checkForMap(g, field, checkerName, checkerVar, eventVar)
		check = "{" + check + "}"
		return check, err
	}

	return checkForKind(g, field, checkerVar, eventVar)
}

// checkForMap returns the checker body for map fields and, as a special case, the
// Pod_Labels field of the Pod message.
func checkForMap(g *protogen.GeneratedFile, field *Field, checkerName, checkerVar, eventVar string) (string, error) {
	// Common part of the map check
	mapCheck := func(eventVar string) (string, error) {
		return `var unmatched []string
            matched := make(map[string]struct{})
            for key, value := range ` + eventVar + ` {
                if len(` + checkerVar + `) > 0 {
                    // Attempt to grab the matcher for this key
                    if matcher, ok := ` + checkerVar + `[key]; ok {
                        if err := matcher.Match(value); err != nil {
                            return ` + common.FmtErrorf(g, field.GoName+"[%s] (%s=%s) check failed: %w", "key", "key", "value", "err") + `
                        }
                        matched[key] = struct{}{}
                    }
                }
            }

            // See if we have any unmatched values that we wanted to match
            if len(matched) != len(` + checkerVar + `) {
                for k := range ` + checkerVar + ` {
                    if _, ok := matched[k]; !ok {
                        unmatched = append(unmatched, k)
                    }
                }
                return ` + common.FmtErrorf(g, field.GoName+" unmatched: %v", "unmatched") + `
            }`, nil
	}

	// Specific to Pod_Labels which needs to be converted into a map
	if field.GoIdent.GoName == "Pod_Labels" {
		splitN := common.GoIdent(g, "strings", "SplitN")
		preamble := `values := make(map[string]string)
        for _, s := range ` + eventVar + ` {
            // Split out key,value pair
            kv := ` + splitN + `(s, "=", 2)
            if len(kv) != 2 {
                // If we wanted to match an invalid label, error out
                if _, ok := ` + checkerVar + `[s]; ok {
                    return ` + common.FmtErrorf(g, checkerName+": Label %s is in an invalid format (want key=value)", "s") + `
                }
                continue
            }
            values[kv[0]] = kv[1]
        }`

		check, err := mapCheck("values")
		if err != nil {
			return "", err
		}
		return preamble + "\n" + check, nil
	}

	return mapCheck(eventVar)
}

// checkForOneof returns the event checker body for a Oneof.
func checkForOneof(g *protogen.GeneratedFile, field *Field, checkerName string, checkerVar string) (string, error) {
	inner, err := checkForKind(g, field, checkerVar, "event."+field.GoName)
	if err != nil {
		return "", err
	}
	fieldIdent := common.TetragonApiIdent(g, field.GoIdent.GoName)
	return `switch event := ` + fmt.Sprintf("%s.%s", eventVarName, field.Oneof.GoName) + `.(type) {
    case *` + fieldIdent + `:
        ` + inner + `
    default:
        return ` + common.FmtErrorf(g, checkerName+": "+field.GoName+" check failed: %T is not a "+field.GoName, "event") + `
    }`, nil
}

// checkForKind returns the event checker body based on the field's protogen kind. For
// maps and oneofs, use check
func checkForKind(g *protogen.GeneratedFile, field *Field, checkerVar, eventVar string) (string, error) {
	kind := field.Desc.Kind()

	// primitive types
	doPrimitiveCheck := func() string {
		ff := kindToFormat(kind)
		if field.IsInnerField {
			return `if ` + checkerVar + ` != ` + eventVar + ` {
                return ` + common.FmtErrorf(g, field.GoName+" has value "+ff+" which does not match expected value "+ff, eventVar, checkerVar) + `
            }`
		}
		return `if *` + checkerVar + ` != ` + eventVar + ` {
            return ` + common.FmtErrorf(g, field.GoName+" has value "+ff+" which does not match expected value "+ff, eventVar, "*"+checkerVar) + `
        }`
	}

	// protobuf wrapper types
	doWrapperCheck := func() string {
		ff := kindToFormat(kind)
		wrapperVal := eventVar + ".Value"
		return `if ` + eventVar + ` == nil {
            return ` + common.FmtErrorf(g, field.GoName+" is nil and does not match expected value "+ff, "*"+checkerVar) + `
        }
        if *` + checkerVar + ` != ` + wrapperVal + ` {
            return ` + common.FmtErrorf(g, field.GoName+" has value "+ff+" which does not match expected value "+ff, wrapperVal, "*"+checkerVar) + `
        }`
	}

	doCheckerCheck := func() string {
		return `if err := ` + checkerVar + `.Check(` + eventVar + `); err != nil {
            return ` + common.FmtErrorf(g, field.GoName+" check failed: %w", "err") + `
        }`
	}

	doEnumCheck := func() string {
		return `if err := ` + checkerVar + `.Check(&` + eventVar + `); err != nil {
            return ` + common.FmtErrorf(g, field.GoName+" check failed: %w", "err") + `
        }`
	}

	doStringCheck := func() string {
		return `if err := ` + checkerVar + `.Match(` + eventVar + `); err != nil {
            return ` + common.FmtErrorf(g, field.GoName+" check failed: %w", "err") + `
        }`
	}

	doBytesCheck := func() string {
		return `if err := ` + checkerVar + `.Match(` + eventVar + `); err != nil {
            return ` + common.FmtErrorf(g, field.GoName+" check failed: %w", "err") + `
        }`
	}

	doTimestampCheck := func() string {
		return `if err := ` + checkerVar + `.Match(` + eventVar + `); err != nil {
            return ` + common.FmtErrorf(g, field.GoName+" check failed: %w", "err") + `
        }`
	}

	doDurationCheck := func() string {
		return `if err := ` + checkerVar + `.Match(` + eventVar + `); err != nil {
            return ` + common.FmtErrorf(g, field.GoName+" check failed: %w", "err") + `
        }`
	}

	switch kind {
	case protoreflect.BoolKind,
		protoreflect.Int32Kind,
		protoreflect.Sint32Kind,
		protoreflect.Uint32Kind,
		protoreflect.Int64Kind,
		protoreflect.Sint64Kind,
		protoreflect.Uint64Kind,
		protoreflect.FloatKind,
		protoreflect.DoubleKind:
		return doPrimitiveCheck(), nil

	case protoreflect.BytesKind:
		return doBytesCheck(), nil

	case protoreflect.StringKind:
		return doStringCheck(), nil

	case protoreflect.MessageKind:
		switch field.Message.GoIdent.GoImportPath {
		case imports.WrappersPath:
			return doWrapperCheck(), nil
		case imports.TimestampPath:
			return doTimestampCheck(), nil
		case imports.DurationPath:
			return doDurationCheck(), nil
		}
		return doCheckerCheck(), nil

	case protoreflect.EnumKind:
		return doEnumCheck(), nil

	default:
		return "", fmt.Errorf("unhandled field type %s (please edit checkForKind in field.go)", kind)
	}
}

// A cache of list checkers we have already generated in order to prevent one from being
// generated twice for the same type
var generatedListChecks = make(map[string]struct{})

func (field *Field) generateListMatcher(g *protogen.GeneratedFile) error {
	// Get the name of the underlying identifier
	var varIdent string
	if msg := field.Message; msg != nil {
		typeImportPath := string(msg.GoIdent.GoImportPath)
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			// NB: not one of our types, skip it
			return nil
		}
		varIdent = common.TetragonApiIdent(g, msg.GoIdent.GoName)
	} else if enum := field.Enum; enum != nil {
		typeImportPath := string(enum.GoIdent.GoImportPath)
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			// NB: not one of our types, skip it
			return nil
		}
		varIdent = common.TetragonApiIdent(g, enum.GoIdent.GoName)
	} else {
		varIdent = field.kind().String()
	}

	if !field.isEnum() && !field.isPrimitive() && field.kind() != protoreflect.StringKind {
		varIdent = "*" + varIdent
	}

	// Get the name of the underlying checker
	var checkerName string
	if name, err := field.typeName(g); err == nil {
		checkerName = name
	} else {
		return err
	}

	// Get the name of the list checker
	listCheckerName := field.listCheckerName(g)

	// Determine if we need to generate a checker still
	if _, ok := generatedListChecks[varIdent]; ok {
		return nil
	}
	generatedListChecks[varIdent] = struct{}{}

	listCheckerKind := common.ListMatcherIdent(g, "Operator")
	kindOrdered := common.ListMatcherIdent(g, "Ordered")
	kindUnordered := common.ListMatcherIdent(g, "Unordered")
	KindSubset := common.ListMatcherIdent(g, "Subset")

	// Generate struct
	g.P(`// ` + listCheckerName + ` checks a list of ` + varIdent + ` fields
    type ` + listCheckerName + ` struct {
        Operator ` + listCheckerKind + ` ` + common.StructTag("json:\"operator\"") + `
        Values ` + checkerName + ` ` + common.StructTag("json:\"values\"") + `
    }`)

	// Generate NewListMatcher
	g.P(`// New` + listCheckerName + ` creates a new ` + listCheckerName + `. The checker defaults to a subset checker unless otherwise specified using WithOperator()
    func New` + listCheckerName + `() *` + listCheckerName + `{
        return &` + listCheckerName + ` {
            Operator: ` + KindSubset + `,
        }
    }`)

	// Generate WithOperator
	g.P(`// WithOperator sets the match kind for the ` + listCheckerName + `
    func (checker *` + listCheckerName + `) WithOperator(operator ` + listCheckerKind + `) *` + listCheckerName + `{
        checker.Operator = operator
        return checker
    }`)

	// Generate WithValues
	checkerNameVarArgs := "..." + strings.TrimPrefix(checkerName, "[]")
	g.P(`// WithValues sets the checkers that the ` + listCheckerName + ` should use
    func (checker *` + listCheckerName + `) WithValues(values ` + checkerNameVarArgs + `) *` + listCheckerName + `{
        checker.Values = values
        return checker
    }`)

	// Generate Check
	g.P(`// Check checks a list of ` + varIdent + ` fields
    func (checker *` + listCheckerName + `) Check(values []` + varIdent + `) error {
        switch checker.Operator {
        case ` + kindOrdered + `:
            return checker.orderedCheck(values)
        case ` + kindUnordered + `:
            return checker.unorderedCheck(values)
        case ` + KindSubset + `:
            return checker.subsetCheck(values)
        default:
            return ` + common.FmtErrorf(g, "Unhandled ListMatcher operator %s", "checker.Operator") + `
        }
    }`)

	var innerCheck string
	var err error
	innerField := &Field{Field: field.Field, IsInnerField: true}
	innerCheck, err = innerField.getFieldCheck(g, listCheckerName, "check", "value")
	if err != nil {
		return err
	}

	innerCheckFn := `func(check ` + strings.TrimPrefix(checkerName, "[]") + `, value ` + varIdent + `) error {
        ` + innerCheck + `
        return nil
    }`

	// Generate orderedCheck
	g.P(`// orderedCheck checks a list of ordered ` + varIdent + ` fields
    func (checker *` + listCheckerName + `) orderedCheck(values []` + varIdent + `) error {
        innerCheck := ` + innerCheckFn + `

        if len(checker.Values) != len(values) {
            return ` + common.FmtErrorf(g, listCheckerName+": Wanted %d elements, got %d", "len(checker.Values)", "len(values)") + `
        }

        for i, check := range checker.Values {
            value := values[i]
            if err := innerCheck(check, value); err != nil {
                return ` + common.FmtErrorf(g, listCheckerName+": Check failed on element %d: %w", "i", "err") + `
            }
        }

        return nil
    }`)

	// Generate unorderedCheck
	g.P(`// unorderedCheck checks a list of unordered ` + varIdent + ` fields
    func (checker *` + listCheckerName + `) unorderedCheck(values []` + varIdent + `) error {
        if len(checker.Values) != len(values) {
            return ` + common.FmtErrorf(g, listCheckerName+": Wanted %d elements, got %d", "len(checker.Values)", "len(values)") + `
        }

        return checker.subsetCheck(values)
    }`)

	// Generate subsetCheck
	g.P(`// subsetCheck checks a subset of ` + varIdent + ` fields
    func (checker *` + listCheckerName + `) subsetCheck(values []` + varIdent + `) error {
        innerCheck := ` + innerCheckFn + `

        numDesired := len(checker.Values)
        numMatched := 0

        nextCheck:
        for _, check := range checker.Values {
            for _, value := range values {
                if err := innerCheck(check, value); err == nil {
                    numMatched += 1
                    continue nextCheck
                }
            }
        }

        if numMatched < numDesired {
            return ` + common.FmtErrorf(g, listCheckerName+": Check failed, only matched %d elements but wanted %d", "numMatched", "numDesired") + `
        }

        return nil
    }`)

	return nil
}

func (field *Field) name() string {
	return field.GoName
}

func (field *Field) listCheckerName(g *protogen.GeneratedFile) string {
	if msg := field.Message; msg != nil {
		typeImportPath := string(field.Message.GoIdent.GoImportPath)
		ret := msg.GoIdent.GoName + "ListMatcher"
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
			ret = g.QualifiedGoIdent(protogen.GoIdent{
				GoName:       ret,
				GoImportPath: protogen.GoImportPath(importPath),
			})
		}
		return ret
	} else if enum := field.Enum; enum != nil {
		typeImportPath := string(field.Enum.GoIdent.GoImportPath)
		ret := enum.GoIdent.GoName + "ListMatcher"
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
			ret = g.QualifiedGoIdent(protogen.GoIdent{
				GoName:       ret,
				GoImportPath: protogen.GoImportPath(importPath),
			})
		}
		return ret
	}
	varIdent := field.kind().String()
	return strcase.ToCamel(varIdent) + "ListMatcher"
}

func (field *Field) newListCheckerName(g *protogen.GeneratedFile) string {
	if msg := field.Message; msg != nil {
		typeImportPath := string(field.Message.GoIdent.GoImportPath)
		ret := fmt.Sprintf("New%sListMatcher", msg.GoIdent.GoName)
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
			ret = g.QualifiedGoIdent(protogen.GoIdent{
				GoName:       ret,
				GoImportPath: protogen.GoImportPath(importPath),
			})
		}
		return ret
	} else if enum := field.Enum; enum != nil {
		typeImportPath := string(field.Enum.GoIdent.GoImportPath)
		ret := fmt.Sprintf("New%sListMatcher", enum.GoIdent.GoName)
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
			ret = g.QualifiedGoIdent(protogen.GoIdent{
				GoName:       ret,
				GoImportPath: protogen.GoImportPath(importPath),
			})
		}
		return ret
	}
	varIdent := field.kind().String()
	return fmt.Sprintf("New%sListMatcher", strcase.ToCamel(varIdent))
}

func (field *Field) kind() protoreflect.Kind {
	kind := field.Desc.Kind()

	switch kind {
	case protoreflect.MessageKind:
		if field.Message.GoIdent.GoImportPath == imports.WrappersPath {
			switch field.Message.GoIdent.GoName {
			case "BoolValue":
				return protoreflect.BoolKind
			case "Int32Value":
				return protoreflect.Int32Kind
			case "Int64Value":
				return protoreflect.Int64Kind
			case "UInt32Value":
				return protoreflect.Uint32Kind
			case "UInt64Value":
				return protoreflect.Uint64Kind
			case "StringValue":
				return protoreflect.StringKind
			case "DoubleValue":
				return protoreflect.DoubleKind
			case "FloatValue":
				return protoreflect.FloatKind
			}
		}
	}

	return kind
}

func (field *Field) isPrimitive() bool {
	kind := field.kind()

	switch kind {
	case protoreflect.BoolKind,
		protoreflect.Int32Kind,
		protoreflect.Sint32Kind,
		protoreflect.Uint32Kind,
		protoreflect.Int64Kind,
		protoreflect.Sint64Kind,
		protoreflect.Uint64Kind,
		protoreflect.FloatKind,
		protoreflect.DoubleKind:
		return true
	}

	return false
}

func (field *Field) isList() bool {
	if field.IsInnerField {
		return false
	}
	if field.GoIdent.GoName == "Pod_Labels" {
		return false
	}
	return field.Desc.IsList()
}

func (field *Field) isMap() bool {
	if field.IsInnerField {
		return false
	}
	if field.GoIdent.GoName == "Pod_Labels" {
		return true
	}
	return field.Desc.IsMap()
}

func (field *Field) isEnum() bool {
	return field.Enum != nil
}

func (field *Field) jsonTag() string {
	return fmt.Sprintf("json:\"%s,omitempty\"", field.Desc.JSONName())
}

func (field *Field) typeName(g *protogen.GeneratedFile) (string, error) {
	kind := field.kind()

	// Pod.Labels is a special case
	if field.GoIdent.GoName == "Pod_Labels" {
		smatcher := common.StringMatcherIdent(g, "StringMatcher")
		return "map[string]" + smatcher, nil
	}

	var type_ string
	switch kind {
	case protoreflect.BoolKind,
		protoreflect.Int32Kind,
		protoreflect.Sint32Kind,
		protoreflect.Uint32Kind,
		protoreflect.Int64Kind,
		protoreflect.Sint64Kind,
		protoreflect.Uint64Kind,
		protoreflect.FloatKind,
		protoreflect.DoubleKind:
		type_ = kind.String()

	case protoreflect.BytesKind:
		bmatcher := common.BytesMatcherIdent(g, "BytesMatcher")
		type_ = bmatcher

	case protoreflect.StringKind:
		smatcher := common.StringMatcherIdent(g, "StringMatcher")
		type_ = smatcher

	case protoreflect.MessageKind:
		switch field.Message.GoIdent.GoImportPath {
		case imports.TimestampPath:
			tsmatcher := common.TimestampMatcherIdent(g, "TimestampMatcher")
			type_ = tsmatcher
		case imports.DurationPath:
			dmatcher := common.DurationMatcherIdent(g, "DurationMatcher")
			type_ = dmatcher
		default:
			type_ = field.Message.GoIdent.GoName + "Checker"
			typeImportPath := string(field.Message.GoIdent.GoImportPath)
			if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
				importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
				type_ = g.QualifiedGoIdent(protogen.GoIdent{
					GoName:       type_,
					GoImportPath: protogen.GoImportPath(importPath),
				})
			}
		}

	case protoreflect.EnumKind:
		type_ = field.Enum.GoIdent.GoName + "Checker"
		typeImportPath := string(field.Enum.GoIdent.GoImportPath)
		if !strings.HasPrefix(typeImportPath, common.TetragonPackageName) {
			importPath := filepath.Join(typeImportPath, "codegen", "eventchecker")
			type_ = g.QualifiedGoIdent(protogen.GoIdent{
				GoName:       type_,
				GoImportPath: protogen.GoImportPath(importPath),
			})
		}

	default:
		return "", fmt.Errorf("unhandled field type %s (please edit checkerTypeName in field.go)", kind)
	}

	if field.isMap() {
		if field.Desc.MapKey().Kind() != protoreflect.StringKind {
			return "", errors.New("maps without string keys are not supported")
		}
		if field.Desc.MapValue().Kind() != protoreflect.StringKind {
			return "", errors.New("maps without string values are not supported")
		}
		return "map[string]" + common.StringMatcherIdent(g, "StringMatcher"), nil
	} else if field.isList() {
		if field.isPrimitive() {
			return "[]" + type_, nil
		}
		return "[]*" + type_, nil
	}

	return type_, nil
}

func kindToFormat(k protoreflect.Kind) string {
	switch k {
	case protoreflect.Int32Kind,
		protoreflect.Sint32Kind,
		protoreflect.Uint32Kind,
		protoreflect.Int64Kind,
		protoreflect.Sint64Kind,
		protoreflect.Uint64Kind:
		return "%d"

	case protoreflect.BoolKind:
		return "%t"

	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return "%f"

	case protoreflect.StringKind:
		return "%s"
	default:
		return "%v"

	}
}
