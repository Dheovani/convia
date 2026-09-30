package secret

import (
	"testing"
)

/*
FuzzParseAcceptsOnlyWhatRenderProduces is the strongest thing that can be said
about a credential parser, and `M24-012` is where it gets said.

Four families of credential pass through [Format.Parse], and every surface in
Convia rests on it refusing the others **on their shape, before any lookup**.
A table test says it refuses the shapes somebody thought of. This says it
refuses everything else: whatever comes out of Parse, put back through Render,
has to be the string that went in.

That closes the gap a table cannot. A parser with slack in it — a tolerated
suffix, a separator it skips, an encoding it normalises — accepts strings the
renderer would never produce, and every one of those is a shape somebody can
present that nobody wrote a case for.
*/
func FuzzParseAcceptsOnlyWhatRenderProduces(f *testing.F) {
	format := Format{Token: "cvk", ID: "cred_"}

	f.Add(format.Render(format.NewID(), New()))
	f.Add("cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
	f.Add("")
	f.Add("cvk__")
	f.Add("cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5_extra")
	f.Add("CVK_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
	f.Add("cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E\n_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")

	f.Fuzz(func(t *testing.T, presented string) {
		id, value, ok := format.Parse(presented)
		if !ok {
			return
		}

		if rendered := format.Render(id, value); rendered != presented {
			t.Fatalf("Parse accepted %q and Render makes %q of what it read — the parser has slack in it",
				presented, rendered)
		}

		// What it read must itself be acceptable, or the identifier it hands a
		// store is one the store's own check would refuse.
		if !format.ValidID(id) {
			t.Fatalf("Parse accepted %q and read the identifier %q, which is not one", presented, id)
		}
		if !ValidRandom(string(value)) {
			t.Fatalf("Parse accepted %q and read a secret that is not this alphabet", presented)
		}
	})
}

/*
FuzzNoFamilyEverAcceptsAnother.

The four families are the only thing between an operator key presented to a
tenant route and it being looked up. The table test covers each family's own
token offered to the other three; this covers everything else a caller might
send, which is where a prefix check with slack in it would show.
*/
func FuzzNoFamilyEverAcceptsAnother(f *testing.F) {
	f.Add("cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
	f.Add("cvo_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
	f.Add("cv_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
	f.Add("cvks_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")

	families := map[string]Format{
		"application": {Token: "cvk", ID: "cred_"},
		"operator":    {Token: "cvo", ID: "oper_"},
		"invitation":  {Token: "cvi", ID: "inv_"},
		"session":     {Token: "cvs", ID: "ses_"},
	}

	f.Fuzz(func(t *testing.T, presented string) {
		accepted := []string{}
		for name, format := range families {
			if _, _, ok := format.Parse(presented); ok {
				accepted = append(accepted, name)
			}
		}

		if len(accepted) > 1 {
			t.Fatalf("%q was accepted as %v, so a credential of one family is one of another",
				presented, accepted)
		}
	})
}
