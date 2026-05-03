package main

import (
	"bytes"
	"fmt"
	"go/format"
	"slices"
	"strings"
	"text/template"
)

type generatedDomain struct {
	PackageName       string
	DomainName        string
	NeedsJSON         bool
	Commands          []generatedCommand
	Events            []generatedEvent
	Enums             []generatedEnum
	AliasTypes        []generatedAliasType
	ReferencedAliases []generatedAliasType
	ObjectTypes       []generatedObjectType
	ReferencedObjects []generatedObjectType
}

type generatedCommand struct {
	CommandConst string
	MethodName   string
	ProtocolName string
	ParamsName   string
	ResultName   string
	HasParams    bool
	HasResult    bool
	Parameters   []generatedField
	Required     []generatedField
	Optional     []generatedField
	Returns      []generatedField
}

type generatedEvent struct {
	EventConst   string
	TypeName     string
	ProtocolName string
	Fields       []generatedField
}

type generatedEnum struct {
	TypeName string
	Values   []generatedEnumValue
}

type generatedEnumValue struct {
	ConstName string
	Value     string
}

type generatedAliasType struct {
	TypeName string
	Type     string
}

type generatedObjectType struct {
	TypeName string
	Fields   []generatedField
}

type generatedField struct {
	Name      string
	Type      string
	JSONName  string
	OmitEmpty bool
	Optional  bool
}

type renderState struct {
	localTypeIndex  map[string]typeSpec
	globalTypeIndex map[string]typeSpec
	built           map[string]generatedObjectType
	building        map[string]bool
}

func renderDomain(domain domainSpec) ([]byte, error) {
	typeIndex := make(map[string]typeSpec, len(domain.Types))
	for _, spec := range domain.Types {
		typeIndex[spec.ID] = spec
	}

	model, err := buildGeneratedDomain(domain, typeIndex)
	if err != nil {
		return nil, err
	}

	tmpl, parseErr := template.New("domain").Funcs(template.FuncMap{
		"lowerFirst": lowerFirst,
	}).Parse(domainTemplate)
	if parseErr != nil {
		return nil, fmt.Errorf("parse domain template: %w", parseErr)
	}

	var buffer bytes.Buffer
	if execErr := tmpl.Execute(&buffer, model); execErr != nil {
		return nil, fmt.Errorf("execute domain template: %w", execErr)
	}

	formatted, err := format.Source(buffer.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated domain: %w\n%s", err, buffer.Bytes())
	}

	return formatted, nil
}

func renderDomainWithTypeIndex(domain domainSpec, globalTypeIndex map[string]typeSpec) ([]byte, error) {
	model, err := buildGeneratedDomain(domain, globalTypeIndex)
	if err != nil {
		return nil, err
	}

	tmpl, parseErr := template.New("domain").Funcs(template.FuncMap{
		"lowerFirst": lowerFirst,
	}).Parse(domainTemplate)
	if parseErr != nil {
		return nil, fmt.Errorf("parse domain template: %w", parseErr)
	}

	var buffer bytes.Buffer
	if execErr := tmpl.Execute(&buffer, model); execErr != nil {
		return nil, fmt.Errorf("execute domain template: %w", execErr)
	}

	formatted, err := format.Source(buffer.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated domain: %w\n%s", err, buffer.Bytes())
	}

	return formatted, nil
}

func buildGeneratedDomain(domain domainSpec, globalTypeIndex map[string]typeSpec) (generatedDomain, error) {
	model := generatedDomain{
		PackageName: lowerFirst(domain.Domain),
		DomainName:  domain.Domain,
		NeedsJSON:   len(domain.Events) > 0,
	}

	localTypeIndex := make(map[string]typeSpec, len(domain.Types))
	for _, spec := range domain.Types {
		localTypeIndex[spec.ID] = spec
		if globalTypeIndex == nil {
			globalTypeIndex = localTypeIndex
		}
	}
	state := renderState{
		localTypeIndex:  localTypeIndex,
		globalTypeIndex: globalTypeIndex,
		built:           make(map[string]generatedObjectType, len(domain.Types)),
		building:        make(map[string]bool, len(domain.Types)),
	}

	for _, spec := range domain.Types {
		switch {
		case len(spec.Enum) > 0:
			model.Enums = mergeEnums(model.Enums, []generatedEnum{{
				TypeName: exportName(spec.ID),
				Values:   buildEnumValues(exportName(spec.ID), spec.Enum),
			}})
		case spec.Type == "object":
			objectType, needsJSON, refs, aliasRefs, enumRefs, err := state.buildObjectType(spec.ID, spec.Properties)
			if err != nil {
				return generatedDomain{}, err
			}
			model.NeedsJSON = model.NeedsJSON || needsJSON
			model.ObjectTypes = append(model.ObjectTypes, objectType)
			model.ReferencedObjects = mergeObjectTypes(model.ReferencedObjects, refs)
			model.ReferencedAliases = mergeAliasTypes(model.ReferencedAliases, aliasRefs)
			model.Enums = mergeEnums(model.Enums, enumRefs)
		default:
			aliasType, needsJSON, err := state.buildAliasType(spec)
			if err != nil {
				return generatedDomain{}, err
			}
			if aliasType.TypeName != "" {
				model.NeedsJSON = model.NeedsJSON || needsJSON
				model.AliasTypes = append(model.AliasTypes, aliasType)
			}
		}
	}

	for _, spec := range domain.Commands {
		command, needsJSON, refs, aliasRefs, enumRefs, err := state.buildCommand(domain.Domain, spec)
		if err != nil {
			return generatedDomain{}, err
		}
		model.NeedsJSON = model.NeedsJSON || needsJSON
		model.Commands = append(model.Commands, command)
		model.ReferencedObjects = mergeObjectTypes(model.ReferencedObjects, refs)
		model.ReferencedAliases = mergeAliasTypes(model.ReferencedAliases, aliasRefs)
		model.Enums = mergeEnums(model.Enums, enumRefs)
	}

	for _, spec := range domain.Events {
		event, needsJSON, refs, aliasRefs, enumRefs, err := state.buildEvent(domain.Domain, spec)
		if err != nil {
			return generatedDomain{}, err
		}
		model.NeedsJSON = model.NeedsJSON || needsJSON
		model.Events = append(model.Events, event)
		model.ReferencedObjects = mergeObjectTypes(model.ReferencedObjects, refs)
		model.ReferencedAliases = mergeAliasTypes(model.ReferencedAliases, aliasRefs)
		model.Enums = mergeEnums(model.Enums, enumRefs)
	}

	slices.SortFunc(model.Enums, func(a generatedEnum, b generatedEnum) int {
		return strings.Compare(a.TypeName, b.TypeName)
	})
	slices.SortFunc(model.AliasTypes, func(a generatedAliasType, b generatedAliasType) int {
		return strings.Compare(a.TypeName, b.TypeName)
	})
	slices.SortFunc(model.ObjectTypes, func(a generatedObjectType, b generatedObjectType) int {
		return strings.Compare(a.TypeName, b.TypeName)
	})
	model.ReferencedAliases = filterNewAliasTypes(model.ReferencedAliases, model.AliasTypes)
	slices.SortFunc(model.ReferencedAliases, func(a generatedAliasType, b generatedAliasType) int {
		return strings.Compare(a.TypeName, b.TypeName)
	})
	model.ReferencedObjects = filterNewObjectTypes(model.ReferencedObjects, model.ObjectTypes)
	slices.SortFunc(model.ReferencedObjects, func(a generatedObjectType, b generatedObjectType) int {
		return strings.Compare(a.TypeName, b.TypeName)
	})

	return model, nil
}

func (state *renderState) buildAliasType(spec typeSpec) (generatedAliasType, bool, error) {
	if spec.ID == "" {
		return generatedAliasType{}, false, nil
	}

	switch spec.Type {
	case "string", "boolean", "integer", "number":
		return generatedAliasType{
			TypeName: exportName(spec.ID),
			Type:     primitiveGoType(spec.Type),
		}, false, nil
	case "array":
		if spec.Items == nil {
			return generatedAliasType{
				TypeName: exportName(spec.ID),
				Type:     "[]any",
			}, false, nil
		}
		itemType, needsJSON, _, _, _, err := state.goTypeForProperty(*spec.Items)
		if err != nil {
			return generatedAliasType{}, false, err
		}
		return generatedAliasType{
			TypeName: exportName(spec.ID),
			Type:     "[]" + itemType,
		}, needsJSON, nil
	default:
		return generatedAliasType{}, false, nil
	}
}

func (state *renderState) buildCommand(domainName string, spec commandSpec) (generatedCommand, bool, []generatedObjectType, []generatedAliasType, []generatedEnum, error) {
	goName := exportName(spec.Name)
	command := generatedCommand{
		CommandConst: "Command" + goName,
		MethodName:   goName,
		ProtocolName: spec.Name,
		ParamsName:   goName + "Params",
		ResultName:   goName + "Result",
		HasParams:    len(spec.Parameters) > 0,
		HasResult:    len(spec.Returns) > 0,
	}

	var needsJSON bool
	var refs []generatedObjectType
	var aliasRefs []generatedAliasType
	var enumRefs []generatedEnum
	for _, param := range spec.Parameters {
		field, fieldNeedsJSON, fieldRefs, fieldAliasRefs, fieldEnumRefs, err := state.buildField(command.ParamsName, param)
		if err != nil {
			return generatedCommand{}, false, nil, nil, nil, err
		}
		needsJSON = needsJSON || fieldNeedsJSON
		refs = mergeObjectTypes(refs, fieldRefs)
		aliasRefs = mergeAliasTypes(aliasRefs, fieldAliasRefs)
		enumRefs = mergeEnums(enumRefs, fieldEnumRefs)
		command.Parameters = append(command.Parameters, field)
		if field.Optional {
			command.Optional = append(command.Optional, field)
		} else {
			command.Required = append(command.Required, field)
		}
	}

	for _, ret := range spec.Returns {
		field, fieldNeedsJSON, fieldRefs, fieldAliasRefs, fieldEnumRefs, err := state.buildField(command.ResultName, ret)
		if err != nil {
			return generatedCommand{}, false, nil, nil, nil, err
		}
		needsJSON = needsJSON || fieldNeedsJSON
		refs = mergeObjectTypes(refs, fieldRefs)
		aliasRefs = mergeAliasTypes(aliasRefs, fieldAliasRefs)
		enumRefs = mergeEnums(enumRefs, fieldEnumRefs)
		command.Returns = append(command.Returns, field)
	}

	return command, needsJSON, refs, aliasRefs, enumRefs, nil
}

func (state *renderState) buildEvent(domainName string, spec eventSpec) (generatedEvent, bool, []generatedObjectType, []generatedAliasType, []generatedEnum, error) {
	goName := exportName(spec.Name)
	event := generatedEvent{
		EventConst:   "Event" + goName,
		TypeName:     goName,
		ProtocolName: spec.Name,
	}

	var needsJSON bool
	var refs []generatedObjectType
	var aliasRefs []generatedAliasType
	var enumRefs []generatedEnum
	for _, param := range spec.Parameters {
		field, fieldNeedsJSON, fieldRefs, fieldAliasRefs, fieldEnumRefs, err := state.buildField(event.TypeName, param)
		if err != nil {
			return generatedEvent{}, false, nil, nil, nil, err
		}
		needsJSON = needsJSON || fieldNeedsJSON
		refs = mergeObjectTypes(refs, fieldRefs)
		aliasRefs = mergeAliasTypes(aliasRefs, fieldAliasRefs)
		enumRefs = mergeEnums(enumRefs, fieldEnumRefs)
		event.Fields = append(event.Fields, field)
	}

	return event, needsJSON, refs, aliasRefs, enumRefs, nil
}

func (state *renderState) buildObjectType(name string, properties []propertySpec) (generatedObjectType, bool, []generatedObjectType, []generatedAliasType, []generatedEnum, error) {
	typeName := exportName(name)
	if objectType, ok := state.built[typeName]; ok {
		return objectType, false, nil, nil, nil, nil
	}
	if state.building[typeName] {
		return generatedObjectType{TypeName: typeName}, false, nil, nil, nil, nil
	}

	state.building[typeName] = true
	defer delete(state.building, typeName)

	objectType := generatedObjectType{TypeName: typeName}
	var needsJSON bool
	var refs []generatedObjectType
	var aliasRefs []generatedAliasType
	var enumRefs []generatedEnum

	for _, property := range properties {
		field, fieldNeedsJSON, fieldRefs, fieldAliasRefs, fieldEnumRefs, err := state.buildField(typeName, property)
		if err != nil {
			return generatedObjectType{}, false, nil, nil, nil, err
		}
		needsJSON = needsJSON || fieldNeedsJSON
		refs = mergeObjectTypes(refs, fieldRefs)
		aliasRefs = mergeAliasTypes(aliasRefs, fieldAliasRefs)
		enumRefs = mergeEnums(enumRefs, fieldEnumRefs)
		objectType.Fields = append(objectType.Fields, field)
	}

	state.built[typeName] = objectType
	return objectType, needsJSON, refs, aliasRefs, enumRefs, nil
}

func (state *renderState) buildField(ownerName string, property propertySpec) (generatedField, bool, []generatedObjectType, []generatedAliasType, []generatedEnum, error) {
	if len(property.Enum) > 0 {
		enumType := exportName(ownerName) + exportName(property.Name)
		return generatedField{
				Name:      exportName(property.Name),
				Type:      enumType,
				JSONName:  property.Name,
				OmitEmpty: property.Optional,
				Optional:  property.Optional,
			}, false, nil, nil, []generatedEnum{{
				TypeName: enumType,
				Values:   buildEnumValues(enumType, property.Enum),
			}}, nil
	}

	fieldType, needsJSON, refs, aliasRefs, enumRefs, err := state.goTypeForProperty(property)
	if err != nil {
		return generatedField{}, false, nil, nil, nil, err
	}
	if state.shouldUsePointerType(ownerName, property) {
		fieldType = "*" + fieldType
	} else if property.Ref != "" && referencesObjectType(refs, exportName(ownerName)) {
		fieldType = "*" + fieldType
	}

	return generatedField{
		Name:      exportName(property.Name),
		Type:      fieldType,
		JSONName:  property.Name,
		OmitEmpty: property.Optional,
		Optional:  property.Optional,
	}, needsJSON, refs, aliasRefs, enumRefs, nil
}

func (state *renderState) shouldUsePointerType(ownerName string, property propertySpec) bool {
	if property.Ref != "" {
		ref := property.Ref
		baseRef := ref
		if strings.Contains(baseRef, ".") {
			baseRef = baseRef[strings.LastIndex(baseRef, ".")+1:]
		}
		spec, ok := state.lookupType(baseRef, ref)
		if !ok || spec.Type != "object" {
			return false
		}

		typeName := exportName(baseRef)
		if property.Optional {
			return true
		}

		if state.building[typeName] {
			return true
		}

		return typeName == exportName(ownerName)
	}

	return property.Optional && property.Type == "object" && len(property.Properties) > 0
}

func (state *renderState) goTypeForProperty(property propertySpec) (string, bool, []generatedObjectType, []generatedAliasType, []generatedEnum, error) {
	if property.Ref != "" {
		crossDomain := strings.Contains(property.Ref, ".")
		ref := property.Ref
		if strings.Contains(ref, ".") {
			ref = ref[strings.LastIndex(ref, ".")+1:]
		}

		spec, ok := state.lookupType(ref, property.Ref)
		if !ok {
			return "string", false, nil, nil, nil, nil
		}

		switch {
		case len(spec.Enum) > 0:
			if crossDomain {
				return "string", false, nil, nil, nil, nil
			}
			return exportName(ref), false, nil, nil, nil, nil
		case spec.Type == "object":
			objectType, needsJSON, refs, aliasRefs, enumRefs, err := state.buildObjectType(ref, spec.Properties)
			if err != nil {
				return "", false, nil, nil, nil, err
			}
			return exportName(ref), needsJSON, mergeObjectTypes([]generatedObjectType{objectType}, refs), aliasRefs, enumRefs, nil
		case spec.Type == "array":
			if spec.Items == nil {
				if crossDomain {
					return "[]any", false, nil, nil, nil, nil
				}
				aliasType, _, err := state.buildAliasType(spec)
				if err != nil {
					return "", false, nil, nil, nil, err
				}
				return exportName(ref), false, nil, []generatedAliasType{aliasType}, nil, nil
			}
			if crossDomain {
				itemType, needsJSON, refs, aliasRefs, enumRefs, err := state.goTypeForProperty(*spec.Items)
				if err != nil {
					return "", false, nil, nil, nil, err
				}
				return "[]" + itemType, needsJSON, refs, aliasRefs, enumRefs, nil
			}
		default:
			if crossDomain {
				return primitiveGoType(spec.Type), false, nil, nil, nil, nil
			}
			aliasType, _, err := state.buildAliasType(spec)
			if err != nil {
				return "", false, nil, nil, nil, err
			}
			return exportName(ref), false, nil, []generatedAliasType{aliasType}, nil, nil
		}
	}

	switch property.Type {
	case "string":
		return "string", false, nil, nil, nil, nil
	case "boolean":
		return "bool", false, nil, nil, nil, nil
	case "integer":
		return "int", false, nil, nil, nil, nil
	case "number":
		return "float64", false, nil, nil, nil, nil
	case "array":
		if property.Items == nil {
			return "[]any", false, nil, nil, nil, nil
		}
		itemType, needsJSON, refs, aliasRefs, enumRefs, err := state.goTypeForProperty(*property.Items)
		if err != nil {
			return "", false, nil, nil, nil, err
		}
		return "[]" + itemType, needsJSON, refs, aliasRefs, enumRefs, nil
	case "object":
		if len(property.Properties) > 0 {
			objectType, needsJSON, refs, aliasRefs, enumRefs, err := state.buildObjectType(property.Name, property.Properties)
			if err != nil {
				return "", false, nil, nil, nil, err
			}
			return exportName(property.Name), needsJSON, mergeObjectTypes([]generatedObjectType{objectType}, refs), aliasRefs, enumRefs, nil
		}
		return "json.RawMessage", true, nil, nil, nil, nil
	default:
		return "any", false, nil, nil, nil, nil
	}
}

func (state *renderState) lookupType(baseRef string, originalRef string) (typeSpec, bool) {
	if strings.Contains(originalRef, ".") {
		if spec, ok := state.globalTypeIndex[originalRef]; ok {
			return spec, true
		}
	}
	if spec, ok := state.localTypeIndex[baseRef]; ok {
		return spec, true
	}
	spec, ok := state.globalTypeIndex[baseRef]
	return spec, ok
}

func primitiveGoType(value string) string {
	switch value {
	case "string":
		return "string"
	case "boolean":
		return "bool"
	case "integer":
		return "int"
	case "number":
		return "float64"
	default:
		return "any"
	}
}

func buildEnumValues(typeName string, values []string) []generatedEnumValue {
	result := make([]generatedEnumValue, 0, len(values))
	for _, value := range values {
		result = append(result, generatedEnumValue{
			ConstName: typeName + exportName(value),
			Value:     value,
		})
	}

	return result
}

func mergeObjectTypes(existing []generatedObjectType, incoming []generatedObjectType) []generatedObjectType {
	index := make(map[string]generatedObjectType, len(existing))
	for _, item := range existing {
		index[item.TypeName] = item
	}

	for _, item := range incoming {
		if _, ok := index[item.TypeName]; !ok {
			index[item.TypeName] = item
			existing = append(existing, item)
		}
	}

	return existing
}

func mergeAliasTypes(existing []generatedAliasType, incoming []generatedAliasType) []generatedAliasType {
	index := make(map[string]generatedAliasType, len(existing))
	for _, item := range existing {
		index[item.TypeName] = item
	}

	for _, item := range incoming {
		if item.TypeName == "" {
			continue
		}
		if _, ok := index[item.TypeName]; !ok {
			index[item.TypeName] = item
			existing = append(existing, item)
		}
	}

	return existing
}

func mergeEnums(existing []generatedEnum, incoming []generatedEnum) []generatedEnum {
	index := make(map[string]generatedEnum, len(existing))
	for _, item := range existing {
		index[item.TypeName] = item
	}

	for _, item := range incoming {
		if item.TypeName == "" {
			continue
		}
		if _, ok := index[item.TypeName]; !ok {
			index[item.TypeName] = item
			existing = append(existing, item)
		}
	}

	return existing
}

func referencesObjectType(items []generatedObjectType, typeName string) bool {
	for _, item := range items {
		if item.TypeName == typeName {
			return true
		}
	}

	return false
}

func exportName(value string) string {
	var builder strings.Builder
	upperNext := true
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if upperNext {
				builder.WriteString(strings.ToUpper(string(r)))
				upperNext = false
			} else {
				builder.WriteRune(r)
			}
		default:
			upperNext = true
		}
	}

	return normalizeInitialisms(builder.String())
}

func normalizeInitialisms(value string) string {
	replacer := strings.NewReplacer(
		"IDentifier", "Identifier",
		"IDentity", "Identity",
		"IDle", "Idle",
		"DOMain", "Domain",
		"Id", "ID",
		"Url", "URL",
		"Api", "API",
		"Css", "CSS",
		"Cpu", "CPU",
		"Dom", "DOM",
		"Guid", "GUID",
		"Html", "HTML",
		"Http", "HTTP",
		"Https", "HTTPS",
		"Js", "JS",
		"Json", "JSON",
		"Uid", "UID",
		"Uuid", "UUID",
		"Xml", "XML",
	)

	return replacer.Replace(value)
}

func filterNewObjectTypes(existing []generatedObjectType, known []generatedObjectType) []generatedObjectType {
	knownNames := make(map[string]struct{}, len(known))
	for _, item := range known {
		knownNames[item.TypeName] = struct{}{}
	}

	filtered := make([]generatedObjectType, 0, len(existing))
	for _, item := range existing {
		if _, ok := knownNames[item.TypeName]; ok {
			continue
		}
		filtered = append(filtered, item)
	}

	return filtered
}

func filterNewAliasTypes(existing []generatedAliasType, known []generatedAliasType) []generatedAliasType {
	knownNames := make(map[string]struct{}, len(known))
	for _, item := range known {
		knownNames[item.TypeName] = struct{}{}
	}

	filtered := make([]generatedAliasType, 0, len(existing))
	for _, item := range existing {
		if _, ok := knownNames[item.TypeName]; ok {
			continue
		}
		filtered = append(filtered, item)
	}

	return filtered
}

const domainTemplate = `// Code generated by cdpgen; DO NOT EDIT.
//
// Package {{ .PackageName }} provides Chrome DevTools Protocol commands and types
// for the {{ .DomainName }} domain.
package {{ .PackageName }}

import (
	"context"
{{- if .NeedsJSON }}
	"encoding/json"
{{- end }}
	"fmt"
)

const (
{{- range .Commands }}
	{{ .CommandConst }} = "{{ $.DomainName }}.{{ .ProtocolName }}"
{{- end }}
{{- range .Events }}
	{{ .EventConst }} = "{{ $.DomainName }}.{{ .ProtocolName }}"
{{- end }}
)

type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

{{- range .Enums }}
type {{ .TypeName }} string

const (
{{- $enum := . }}
{{- range .Values }}
	{{ .ConstName }} {{ $enum.TypeName }} = "{{ .Value }}"
{{- end }}
)

{{- end }}
{{- range .AliasTypes }}
type {{ .TypeName }} {{ .Type }}

{{- end }}
{{- range .ReferencedAliases }}
type {{ .TypeName }} {{ .Type }}

{{- end }}
{{- range .ObjectTypes }}
type {{ .TypeName }} struct {
{{- range .Fields }}
	{{ .Name }} {{ .Type }} ` + "`json:\"{{ .JSONName }}{{ if .OmitEmpty }},omitempty{{ end }}\"`" + `
{{- end }}
}

{{- end }}
{{- range .ReferencedObjects }}
type {{ .TypeName }} struct {
{{- range .Fields }}
	{{ .Name }} {{ .Type }} ` + "`json:\"{{ .JSONName }}{{ if .OmitEmpty }},omitempty{{ end }}\"`" + `
{{- end }}
}

{{- end }}
{{- range .Commands }}
{{- $command := . }}
type {{ .ParamsName }} struct {
{{- range .Parameters }}
	{{ .Name }} {{ .Type }} ` + "`json:\"{{ .JSONName }}{{ if .OmitEmpty }},omitempty{{ end }}\"`" + `
{{- end }}
}

{{- if .HasResult }}
type {{ .ResultName }} struct {
{{- range .Returns }}
	{{ .Name }} {{ .Type }} ` + "`json:\"{{ .JSONName }}{{ if .OmitEmpty }},omitempty{{ end }}\"`" + `
{{- end }}
}

{{- end }}
func {{ .MethodName }}({{ if .HasParams }}{{- range $index, $field := .Required }}{{ if $index }}, {{ end }}{{ lowerFirst $field.Name }} {{ $field.Type }}{{ end }}{{ end }}) *{{ .ParamsName }} {
	return &{{ .ParamsName }}{ {{- range $index, $field := .Required }}{{ if $index }}, {{ end }}{{ $field.Name }}: {{ lowerFirst $field.Name }}{{ end }} }
}

{{- range .Optional }}
func (p {{ $command.ParamsName }}) With{{ .Name }}({{ lowerFirst .Name }} {{ .Type }}) *{{ $command.ParamsName }} {
	p.{{ .Name }} = {{ lowerFirst .Name }}
	return &p
}

{{- end }}

{{- if .HasResult }}
func (p *{{ .ParamsName }}) Do(ctx context.Context, caller Caller) (*{{ .ResultName }}, error) {
	if caller == nil {
		return nil, fmt.Errorf("execute %s: nil caller", {{ .CommandConst }})
	}

	var result {{ .ResultName }}
	if err := caller.Call(ctx, {{ .CommandConst }}, p, &result); err != nil {
		return nil, fmt.Errorf("execute %s: %w", {{ .CommandConst }}, err)
	}

	return &result, nil
}
{{- else }}
func (p *{{ .ParamsName }}) Do(ctx context.Context, caller Caller) error {
	if caller == nil {
		return fmt.Errorf("execute %s: nil caller", {{ .CommandConst }})
	}

	if err := caller.Call(ctx, {{ .CommandConst }}, p, nil); err != nil {
		return fmt.Errorf("execute %s: %w", {{ .CommandConst }}, err)
	}

	return nil
}
{{- end }}

{{- end }}
{{- range .Events }}
type {{ .TypeName }} struct {
{{- range .Fields }}
	{{ .Name }} {{ .Type }} ` + "`json:\"{{ .JSONName }}{{ if .OmitEmpty }},omitempty{{ end }}\"`" + `
{{- end }}
}

func Decode{{ .TypeName }}(data json.RawMessage) (*{{ .TypeName }}, error) {
	var event {{ .TypeName }}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("decode %s: %w", {{ .EventConst }}, err)
	}

	return &event, nil
}

{{- end }}
`
