import '@testing-library/jest-dom/vitest'

/*
jsdom implements no layout, so it has no scrollIntoView.

This is a gap in the test environment rather than one in the interface, and it
is stubbed here rather than guarded at the call site: a `typeof … === 'function'`
check in the component would be a branch that exists only because of the
renderer these tests use, and it would quietly hide the day the call really did
stop working.
*/
Element.prototype.scrollIntoView = function scrollIntoView() {}
