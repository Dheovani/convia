package media

import "context"

// reportKey is private so that only this package can put a report in a context.
type reportKey struct{}

/*
ContextWithReport records a report whose origin was verified.

It is set by the middleware that checked the signature and by nothing else, so a
handler that finds one knows it came from the media plane.
*/
func ContextWithReport(ctx context.Context, report Report) context.Context {
	return context.WithValue(ctx, reportKey{}, report)
}

// ReportFromContext returns the verified report the request carried, if any.
func ReportFromContext(ctx context.Context) (Report, bool) {
	report, found := ctx.Value(reportKey{}).(Report)
	return report, found
}
