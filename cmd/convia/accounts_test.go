package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

/*
TestCreatingAnAccountHandsOverThePassword is a regression test for a fix that
makes things worse, and for the second fix that got past the first version of
this test.

A static analyser flags `convia account create` for writing a credential to
standard output. It does, and it must: Convia generates the password, stores
only a one-way digest of it, and has no password reset — that needs a mailer,
which docs/sessions.md names as a gap rather than a plan. The finding has been
"fixed" twice. The first time the value was discarded at the call (`account, _,
err`); every account created in that window is unusable forever. The second time
it was kept and thrown away a line later with `_ = password`, which is a use of
the identifier, which is all the first version of this test asked for.

So it no longer asks whether the password is *used*. It asks whether it reaches
**standard output**, which is the only thing that makes the command work, and it
names the discard shape explicitly because that is the one that has happened.

The property is asserted on the source rather than on behaviour, because the
failure mode is a source edit. There is no database here and none is needed.
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
		t.Fatal("createAccount discards the generated password at the call. The account it " +
			"creates can never be signed in to: the digest is one-way and Convia has no " +
			"password reset. If a static analyser asked for this, the answer is to dismiss " +
			"the finding, not to accept it.")
	}

	if discards(create, bound) {
		t.Errorf("createAccount binds the password as %q and then throws it away with "+
			"`_ = %s`. That is the same defect as discarding it at the call, written so "+
			"that the identifier still looks used: the command prints no password, and "+
			"nothing can recover one.", bound, bound)
	}

	if !printed(create, bound) {
		t.Errorf("createAccount never passes %q to fmt. The password is shown once and stored "+
			"only as a digest, so a run that does not print it creates an account nobody "+
			"can ever sign in to.", bound)
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

// discards reports the `_ = name` shape: a binding kept only so that the
// compiler stops complaining about one that is never read.
func discards(function *ast.FuncDecl, name string) bool {
	thrownAway := false

	ast.Inspect(function, func(node ast.Node) bool {
		assignment, isAssignment := node.(*ast.AssignStmt)
		if !isAssignment || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}

		blank, isBlank := assignment.Lhs[0].(*ast.Ident)
		value, isValue := assignment.Rhs[0].(*ast.Ident)
		if isBlank && blank.Name == "_" && isValue && value.Name == name {
			thrownAway = true
			return false
		}
		return true
	})

	return thrownAway
}

/*
printed reports whether the identifier reaches a call on fmt.

It looks inside the arguments rather than at them, because the password is a
redacted type and reaching output means converting it first: `string(password)`
is the argument, and `password` is inside it.
*/
func printed(function *ast.FuncDecl, name string) bool {
	reached := false

	ast.Inspect(function, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}

		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		if !isSelector {
			return true
		}
		package_, isPackage := selector.X.(*ast.Ident)
		if !isPackage || package_.Name != "fmt" {
			return true
		}

		for _, argument := range call.Args {
			ast.Inspect(argument, func(inner ast.Node) bool {
				if identifier, isIdentifier := inner.(*ast.Ident); isIdentifier &&
					identifier.Name == name {
					reached = true
				}
				return !reached
			})
		}
		return !reached
	})

	return reached
}
