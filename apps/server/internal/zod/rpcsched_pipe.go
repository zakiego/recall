package zod

import "github.com/zakiego/recall/apps/server/internal/jsonx"

// TransformPipeS is in.transform(fn).pipe(out): parse with in, and only when
// that has no issues, map the value with fn and parse the result with out.
// Issues from either side keep their paths, as zod's ZodPipe does.
type TransformPipeS struct {
	in, out Schema
	fn      func(any) any
}

// TransformPipe is in.transform(fn).pipe(out), e.g. outboundSettingsSchema's
// signature: z.string().transform(normalize).pipe(z.string().max(150)).
func TransformPipe(in Schema, fn func(any) any, out Schema) *TransformPipeS {
	return &TransformPipeS{in: in, out: out, fn: fn}
}

func (s *TransformPipeS) Optional() *OptionalS { return Optional(s) }
func (s *TransformPipeS) optin() string        { return s.in.optin() }
func (s *TransformPipeS) optout() string       { return s.out.optout() }
func (s *TransformPipeS) json() *jsonx.Object  { return s.in.json() }
func (s *TransformPipeS) parse(v any) (any, []Issue) {
	mid, issues := s.in.parse(v)
	if len(issues) > 0 {
		return mid, issues
	}
	return s.out.parse(s.fn(mid))
}
