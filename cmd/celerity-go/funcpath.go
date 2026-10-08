package main

import (
	"fmt"
	"go/ast"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// A handler is found by the name the runtime knows it as, which is what the
// manifest records and what ties the analysis to the function the binary will
// actually dispatch to. Matching on anything else, such as the route or the
// resource name, would analyse whatever the source looks like rather than what
// was registered.
//
// runtime.FuncForPC names a function in one of three shapes:
//
//	example.com/app/orders.Create             a function
//	example.com/app/orders.(*Service).Get     a method
//	example.com/app/orders.(*Service).Get-fm  a method value
//	example.com/app.main.func1                a function literal
//
// and nests the last, so a literal inside a literal is func1.1. Which is what
// has to be unpicked to find the body to walk.
//
// The -fm suffix is the wrapper the compiler makes when a method is used as a
// value, which is what registering service.Create rather than a literal
// produces. It names the same body, so it is taken off.

// methodValueSuffix is what the compiler appends to the wrapper it makes when
// a method is used as a value rather than called.
const methodValueSuffix = "-fm"

// A function as the runtime names it, split into the package it is
// in and the steps to reach it.
type funcPath struct {
	pkg string
	// steps is the declaration first and then one entry per function literal:
	// ["main", "func1"] is the first literal in main.
	steps []string
}

// Splits a runtime function name.
//
// The package path is everything before the first dot of the last path
// segment, since a package path may contain dots of its own:
// example.com/app.main has a dot in the domain and another before main.
func parseFuncPath(name string) (funcPath, error) {
	segmentStart := strings.LastIndex(name, "/") + 1
	dot := strings.Index(name[segmentStart:], ".")
	if dot < 0 {
		return funcPath{}, fmt.Errorf("%q names no package", name)
	}
	dot += segmentStart

	path := funcPath{pkg: name[:dot]}
	path.steps = append(path.steps, splitSteps(name[dot+1:])...)
	if len(path.steps) == 0 {
		return funcPath{}, fmt.Errorf("%q names no function", name)
	}
	return path, nil
}

// Splits a runtime name into the declaration and the function
// literals inside it.
//
// A receiver and the method on it are one step, because the dot between them
// separates a type from its method rather than one step from the next:
// (*Service).Get is one function, and (*Service).Get.func1 is a literal inside
// it.
func splitSteps(rest string) []string {
	if !strings.HasPrefix(rest, "(") {
		return strings.Split(rest, ".")
	}

	closing := strings.Index(rest, ")")
	if closing < 0 {
		return strings.Split(rest, ".")
	}

	after := strings.Split(strings.TrimPrefix(rest[closing+1:], "."), ".")
	steps := []string{rest[:closing+1] + "." + after[0]}
	return append(steps, after[1:]...)
}

// Finds the function body a path names, and reports nothing where the
// package it names was not loaded.
//
// A handler in a package the analysis did not load is not an error: a build
// loads the application's own packages, and a handler registered from a library
// is one whose resources that library's own build is responsible for.
func bodyOf(loaded map[string]*packages.Package, path funcPath) (*ast.FuncDecl, ast.Node, bool) {
	pkg, ok := loaded[path.pkg]
	if !ok {
		return nil, nil, false
	}

	decl, found := declarationNamed(pkg, path.steps[0])
	if !found {
		return nil, nil, false
	}

	var body ast.Node = decl.Body
	for _, step := range path.steps[1:] {
		literal, ok := literalNamed(body, step)
		if !ok {
			return nil, nil, false
		}
		body = literal
	}
	return decl, body, true
}

// Finds a function or method declaration by the name the
// runtime gives it.
func declarationNamed(pkg *packages.Package, step string) (*ast.FuncDecl, bool) {
	receiver, name := splitReceiver(step)

	for _, file := range pkg.Syntax {
		for _, declaration := range file.Decls {
			fn, isFunc := declaration.(*ast.FuncDecl)
			if !isFunc || fn.Name.Name != name || fn.Body == nil {
				continue
			}
			if receiverName(fn) == receiver {
				return fn, true
			}
		}
	}
	return nil, false
}

// Takes the receiver out of a step, so that (*Service).Get is a
// "Get" on "Service" and "Create" is a function.
func splitReceiver(step string) (receiver, name string) {
	if !strings.HasPrefix(step, "(") {
		return "", strings.TrimSuffix(step, methodValueSuffix)
	}
	close := strings.Index(step, ")")
	if close < 0 {
		return "", step
	}
	receiver = strings.TrimPrefix(step[1:close], "*")
	name = strings.TrimPrefix(step[close+1:], ".")
	return receiver, strings.TrimSuffix(name, methodValueSuffix)
}

// The type a method is on, without the pointer, and empty for
// a function.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}

	switch typ := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if named, ok := typ.X.(*ast.Ident); ok {
			return named.Name
		}
	case *ast.Ident:
		return typ.Name
	case *ast.IndexExpr:
		// A method on a generic type, whose receiver is Type[T].
		if named, ok := typ.X.(*ast.Ident); ok {
			return named.Name
		}
	}
	return ""
}

// Finds the function literal a funcN step names.
//
// The runtime numbers them in the order they appear in the enclosing body,
// counting only the ones directly inside it, a literal nested in another is
// reached by the next step rather than counted here.
func literalNamed(body ast.Node, step string) (*ast.FuncLit, bool) {
	index, err := strconv.Atoi(strings.TrimPrefix(step, "func"))
	if err != nil || index < 1 {
		return nil, false
	}

	var found *ast.FuncLit
	seen := 0
	ast.Inspect(body, func(node ast.Node) bool {
		literal, isLiteral := node.(*ast.FuncLit)
		if !isLiteral {
			return found == nil
		}

		seen += 1
		if seen == index {
			found = literal
		}

		// Do not go deeper, a literal inside this function literal
		// is the next step's to find,
		// and counting it here would shift every number after it.
		return false
	})
	return found, found != nil
}
