package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

/*
TestCreatingAnAccountHandsOverThePassword is a regression test for a fix that
made things worse.

A static analyser flagged `convia account create` for writing a credential to
standard output. It does, and it must: Convia generates the password, stores
only a one-way digest of it, and has no password reset — that needs a mailer,
which is named in docs/sessions.md as a gap rather than a plan. The suggested
fix was accepted, and for one release every account the command created was
unusable the moment it was created. Nobody could sign in, and no flow existed to
recover, because the message the fix printed described a flow that is not there.

So the property is asserted on the source rather than on behaviour, because the
failure mode is a source edit: an autofix replacing the password with `_`. There
is no database here and none is needed — what has to hold is that the value
Convia hands back is used rather than discarded.
*/
func TestCreatingAnAccountHandsOverThePassword(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "accounts.go", nil, 0)
	if err != nil {
		t.Fatalf("parse accounts.go: %v", err)
	}

	create := functionNamed(file, "createAccount")
	if create == nil {
		t.Fatal("accounts.go declares no createAccount")
	}

	bound := boundName(create, "Create")
	switch bound {
	case "":
		t.Fatal("createAccount no longer calls Create, so it cannot be handing over a password")
	case "_":
		t.Fatal("createAccount discards the generated password. The account it creates can " +
			"never be signed in to: the digest is one-way and Convia has no password reset. " +
			"If a static analyser asked for this, the answer is to dismiss the finding, not " +
			"to accept it.")
	}

	if !mentions(create, bound) {
		t.Errorf("createAccount takes the password as %q and never uses it", bound)
	}
}

func functionNamed(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if isFunction && function.Name.Name == name {
			return function
		}
	}
	return nil
}

/*
boundName returns the identifier the second result of a call to method is
assigned to.

Create answers with the account, the password, and an error. The middle one is
what this is about, and its name is whatever the caller chose — so the test
finds it rather than assuming it.
*/
func boundName(function *ast.FuncDecl, method string) string {
	found := ""

	ast.Inspect(function, func(node ast.Node) bool {
		assignment, isAssignment := node.(*ast.AssignStmt)
		if !isAssignment || len(assignment.Lhs) != 3 || len(assignment.Rhs) != 1 {
			return true
		}

		call, isCall := assignment.Rhs[0].(*ast.CallExpr)
		if !isCall {
			return true
		}
		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		if !isSelector || selector.Sel.Name != method {
			return true
		}

		if name, isName := assignment.Lhs[1].(*ast.Ident); isName {
			found = name.Name
		}
		return false
	})

	return found
}

// mentions reports whether the function reads an identifier anywhere after
// binding it.
func mentions(function *ast.FuncDecl, name string) bool {
	uses := 0

	ast.Inspect(function, func(node ast.Node) bool {
		if identifier, isIdentifier := node.(*ast.Ident); isIdentifier && identifier.Name == name {
			uses++
		}
		return true
	})

	// One use is the binding itself; a second is somebody doing something with it.
	return uses > 1
}
