package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

// This file is the mechanical guard for issue #87: retry eligibility is a
// property of the ENDPOINT, not the HTTP method. PUT /posts/import creates
// posts, so it must never retry even though doPUT retries for full-state
// Update* calls. The decision is declared per call site via the retryable
// argument on doGET/doPUT/doDELETE (compiler-enforced — no default, so a new
// call site cannot silently inherit its method's behaviour) and pinned here.
//
// Two layers, both required:
//
//  1. TestRetryPolicySweep enumerates EVERY exported *Client method via
//     reflection and asserts it has a declared retry policy in retryPolicies.
//     A method with no declaration → RED, naming the method. This is the gate
//     that stops the retry surface widening again the next time someone adds
//     an endpoint — without it the only thing standing in the way would be a
//     comment, which is precisely how #87 happened.
//
//  2. The TestRetryPolicy_*_NotRetried / _Retried pair behaviourally pins the
//     invariant: a 500-then-200 stub must yield exactly ONE request for every
//     create-shaped PUT call site (ImportSearchPost, CopySearchPost, and the
//     createPostWithMode dispatcher that all 16 CrossPost* wrappers funnel
//     through) and TWO for a full-state Update* PUT. Reverting any create
//     call site's retryable flag to true makes its test RED.

// retryPolicy is the declared retry behaviour of a request-issuing *Client
// method. The declaration lives in retryPolicies and is enforced by
// TestRetryPolicySweep; the create-never-retries half is also pinned
// behaviourally by the TestRetryPolicy_* tests below.
type retryPolicy int

const (
	// retryAllowed: an idempotent call (GET read, DELETE, or a full-state PUT
	// update to a known id) that may retry on 429/5xx when RetryOptions is set.
	retryAllowed retryPolicy = iota
	// retryNever: a non-idempotent call (POST create, multipart upload, raw
	// doGETRaw/doPUTRaw, or a create-shaped PUT) that must never retry — a
	// retry after a committed write would duplicate created objects.
	retryNever
	// retryComposite: the method does not issue a request directly; it
	// delegates to other *Client methods whose own policies govern. Its retry
	// behaviour is the union of its callees' (e.g. ListAllPosts loops ListPosts,
	// which is retryAllowed).
	retryComposite
)

// retryPolicies is the single declaration table. Every exported *Client
// method MUST be listed here — TestRetryPolicySweep fails on any omission or
// stale entry. A create-shaped method (Create*, Copy*, Import*, Upload*, the
// CrossPost* family, BatchDeletePosts, StartParsing, UpdateScheduleFromEdit)
// MUST be retryNever; the sweep's consistency check enforces that a method
// declaring creates=true is retryNever, so a create mis-declared as
// retryAllowed is RED.
//
// The table is the human-readable declaration; the retryable flag at each
// do* call site is the compiler-enforced decision. The behavioural tests
// below pin that the two agree for the create-shaped PUTs (the #87 hazard)
// and a representative update.
var retryPolicies = map[string]struct {
	policy retryPolicy
	// creates is true when the method creates a new object (non-idempotent —
	// a retry after a committed write duplicates it). The sweep asserts
	// creates => policy == retryNever.
	creates bool
}{
	// --- watermarks ---
	"ListWatermarks":             {retryAllowed, false},
	"ListAllWatermarks":          {retryComposite, false},
	"ListAllWatermarksWithTotal": {retryComposite, false},
	"CreateWatermark":            {retryNever, true},
	"UpdateWatermark":            {retryAllowed, false},
	"DeleteWatermark":            {retryAllowed, false},
	// --- user/settings ---
	"GetUser":     {retryAllowed, false},
	"GetSettings": {retryAllowed, false},
	// --- proxies ---
	"ListProxies":             {retryAllowed, false},
	"ListAllProxies":          {retryComposite, false},
	"ListAllProxiesWithTotal": {retryComposite, false},
	"CreateProxy":             {retryNever, true},
	"UpdateProxy":             {retryAllowed, false},
	"DeleteProxy":             {retryAllowed, false},
	// --- projects + schedules ---
	"ListProjects":              {retryAllowed, false},
	"UpdateProject":             {retryAllowed, false},
	"CreateProject":             {retryNever, true},
	"DeleteProject":             {retryAllowed, false},
	"ListSchedules":             {retryAllowed, false},
	"ListAllSchedules":          {retryComposite, false},
	"ListAllSchedulesWithTotal": {retryComposite, false},
	"ListAllProjects":           {retryComposite, false},
	"ListAllProjectsWithTotal":  {retryComposite, false},
	"CreateSchedule":            {retryNever, true},
	"UpdateSchedule":            {retryAllowed, false},
	// UpdateScheduleFromEdit uses doGETRaw + doPUTRaw, which never retry
	// (no RetryOptions path). It is a full-state update semantically, but its
	// transports are non-retrying by construction, so the declared policy is
	// retryNever matching the actual behaviour.
	"UpdateScheduleFromEdit": {retryNever, false},
	"DeleteSchedule":         {retryAllowed, false},
	"GetScheduleEdit":        {retryAllowed, false},
	// --- posts search (scraping) ---
	"ListSearchPosts":                         {retryAllowed, false},
	"ListAllSearchPosts":                      {retryComposite, false},
	"ListAllSearchPostsWithTotal":             {retryComposite, false},
	"ListAllSearchPostsWithFirstAndLastTotal": {retryComposite, false},
	"ListSourceResources":                     {retryAllowed, false},
	"ListAllSourceResources":                  {retryComposite, false},
	"ListAllSourceResourcesWithTotal":         {retryComposite, false},
	"GetParsingForm":                          {retryAllowed, false},
	"StartParsing":                            {retryNever, true},
	"StopParsing":                             {retryAllowed, false},
	"CopySearchPost":                          {retryNever, true}, // PUT /posts/copy creates a copy
	"GetSearchPostEdit":                       {retryAllowed, false},
	"RewriteSearchPost":                       {retryNever, true}, // POST /posts with as_copy=1 creates
	"ImportSearchPost":                        {retryNever, true}, // PUT /posts/import creates — the #87 case
	// --- posts ---
	"ListPosts":                         {retryAllowed, false},
	"ListAllPosts":                      {retryComposite, false},
	"ListAllPostsWithTotal":             {retryComposite, false},
	"ListAllPostsWithFirstAndLastTotal": {retryComposite, false},
	"CreatePost":                        {retryNever, true},
	"UpdatePost":                        {retryAllowed, false},
	"GetPostEdit":                       {retryAllowed, false},
	"UpdatePostText":                    {retryComposite, false}, // delegates to GetPostEdit + UpdatePost
	"DeletePost":                        {retryAllowed, false},
	"BatchDeletePosts":                  {retryNever, true}, // POST /posts/batch/delete
	// MovePost is composite: GetPostEdit (retryAllowed) + UpdatePost
	// (retryAllowed full-state PUT) + GetPostEdit (date recovery). The move
	// itself is a full-state PUT to a known id, which converges on re-send;
	// the date-recovery read is idempotent. No create — the post already
	// exists, only its schedule_id changes.
	"MovePost": {retryComposite, false},
	// BatchMovePosts issues POST /posts/batch/move via doPOST, which has no
	// retryable param and never retries — so the declared policy is
	// retryNever matching the actual behaviour. The endpoint IS idempotent
	// (moving to the same schedule twice is the same end state), but doPOST
	// cannot express that; if it gained a retryable param, idempotency
	// would make this retryAllowed. The post-move per-id GetPostEdit reads
	// are retryAllowed but happen after the POST commits, so they do not
	// change the create classification (creates=false).
	"BatchMovePosts": {retryNever, false},
	// ListSchedulePosts is a GET read — retryAllowed.
	"ListSchedulePosts": {retryAllowed, false},
	// --- notifications ---
	"ListNotifications":                         {retryAllowed, false},
	"ListAllNotifications":                      {retryComposite, false},
	"ListAllNotificationsWithTotal":             {retryComposite, false},
	"ListAllNotificationsWithFirstAndLastTotal": {retryComposite, false},
	// --- files (multipart streaming uploads — never retry) ---
	"UploadMedia":    {retryNever, true},
	"UploadDocument": {retryNever, true},
	// --- doctor (composite: calls many read/list methods) ---
	"RunDoctor": {retryComposite, false},
	// --- crosspost family: every /posts/{mode} endpoint CREATES a post via
	//     createPostWithMode (PUT retryable=false). All 16 are retryNever. ---
	"CrossPostWithMode": {retryNever, true},
	"SearchPosts":       {retryNever, true},
	"CopyPost":          {retryNever, true},
	"SourcesPost":       {retryNever, true},
	"ImportPost":        {retryNever, true},
	"CrossPost":         {retryNever, true},
	"RewritePost":       {retryNever, true},
	"TranslatePost":     {retryNever, true},
	"QueuePost":         {retryNever, true},
	"DraftPost":         {retryNever, true},
	"TemplatePost":      {retryNever, true},
	"RSSPost":           {retryNever, true},
	"FeedPost":          {retryNever, true},
	"TagPost":           {retryNever, true},
	"WatermarkPost":     {retryNever, true},
	"BatchPost":         {retryNever, true},
	// --- accounts / pages ---
	"ListAccounts":                      {retryAllowed, false},
	"ListAllAccounts":                   {retryComposite, false},
	"ListAllAccountsWithTotal":          {retryComposite, false},
	"ListPages":                         {retryAllowed, false},
	"ListAllPages":                      {retryComposite, false},
	"ListAllPagesWithTotal":             {retryComposite, false},
	"ListAllPagesWithFirstAndLastTotal": {retryComposite, false},
	"DisconnectPage":                    {retryAllowed, false},
}

// TestRetryPolicySweep is the mechanical guard for issue #87. It enumerates
// every exported *Client method via reflection and asserts:
//  1. Completeness — every method is declared in retryPolicies (a new endpoint
//     added with no declaration is RED, naming the method). This is the gate
//     that prevents the retry surface widening silently.
//  2. No stale entries — every table entry is a real method.
//  3. Consistency — a method declaring creates=true MUST be retryNever (a
//     create mis-declared retryable is RED).
//
// The sweep forces a CLASSIFICATION, not a verification: it guarantees no
// method ships UNDECLARED. The behavioural tests below verify the
// create-shaped PUT call sites actually pass retryable=false.
func TestRetryPolicySweep(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil))
	declared := make(map[string]bool, len(retryPolicies))

	// 1. Completeness: every exported *Client method must be declared.
	for i := 0; i < clientType.NumMethod(); i++ {
		name := clientType.Method(i).Name
		if name == "String" { // defensive: no String method exists today, but skip any fmt-panics if added.
			continue
		}
		spec, ok := retryPolicies[name]
		if !ok {
			t.Errorf("method %q on *Client is not declared in retryPolicies — every request-issuing method MUST declare a retry policy (this test prevents a recurrence of issue #87: a create-shaped endpoint silently inheriting its method's retry behaviour)", name)
			continue
		}
		declared[name] = true
		// 3. Consistency: a create must never be retryable.
		if spec.creates && spec.policy != retryNever {
			t.Errorf("method %q is declared creates=true but policy != retryNever — a method that creates an object MUST NOT be retryable (a retry after a committed write would duplicate it)", name)
		}
	}

	// 2. No stale entries: every table entry must be a real method.
	for name := range retryPolicies {
		if _, ok := clientType.MethodByName(name); !ok {
			t.Errorf("retryPolicies declares %q but *Client has no such method — stale table entry", name)
		}
	}
}

// stub500Then200 returns a server that responds 500 for the first n-1 calls
// and 200 with the given body for the nth, counting every request.
func stub500Then200(t *testing.T, okBody string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(okBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestRetryPolicy_CreateNotRetried pins the #87 case: ImportSearchPost
// (PUT /posts/import) CREATES posts, so a 500-then-200 must yield exactly ONE
// request. Reverting its doPUT retryable flag to true makes this RED (calls==2
// and the create is duplicated).
func TestRetryPolicy_CreateNotRetried(t *testing.T) {
	srv, calls := stub500Then200(t, `{"id":123}`)
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// when_type=1 avoids the schedule guard and the before-snapshot GET, so
	// the only request is the create PUT itself. With retryable=false the
	// first 500 is returned as an error (no retry to the 200) — that is the
	// invariant: even when a retry would "succeed", the create must not take
	// it, because the server may have committed the write before the 500.
	_, err = c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{1},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{1},
	})
	if err == nil {
		t.Fatal("ImportSearchPost: expected the 500 to surface as an error (no retry), got nil — the create must not retry to the 200 even though one is queued")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ImportSearchPost issued %d requests, want 1 — PUT /posts/import creates posts and MUST NOT retry (reverting doPUT retryable to true makes this 2 and duplicates the write)", got)
	}
}

// TestRetryPolicy_CopySearchPost_NotRetried pins the second create-shaped PUT
// the prior fix missed: CopySearchPost (PUT /posts/copy) CREATES a copy of a
// scraped post. A 500-then-200 must yield ONE request.
func TestRetryPolicy_CopySearchPost_NotRetried(t *testing.T) {
	srv, calls := stub500Then200(t, `{"id":123}`)
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Scalar SearchPostID (the slice is refused by CopySearchPost); when_type=1
	// avoids the schedule guard and snapshot GET. The first 500 surfaces as an
	// error (no retry to the queued 200).
	_, err = c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1,
		PublicationWhenType: 1,
	})
	if err == nil {
		t.Fatal("CopySearchPost: expected the 500 to surface as an error (no retry), got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("CopySearchPost issued %d requests, want 1 — PUT /posts/copy creates a post and MUST NOT retry", got)
	}
}

// TestRetryPolicy_CrosspostMode_NotRetried pins the third create-shaped PUT
// path: every CrossPost* wrapper funnels through createPostWithMode
// (PUT /posts/{mode}), which CREATES a post. CopyPost stands in for all 16;
// they share one doPUT call site (retryable=false), so this covers the family.
func TestRetryPolicy_CrosspostMode_NotRetried(t *testing.T) {
	srv, calls := stub500Then200(t, `{"id":123}`)
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.CopyPost(context.Background(), map[string]interface{}{"text": "hi"}); err == nil {
		t.Fatal("CopyPost: expected the 500 to surface as an error (no retry), got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("CopyPost (createPostWithMode) issued %d requests, want 1 — PUT /posts/{mode} creates a post and MUST NOT retry", got)
	}
}

// TestRetryPolicy_UpdateRetried pins the other half: a full-state PUT update
// to a known id (UpdateWatermark → PUT /watermarks/{id}) MUST still retry on
// 500-then-200 (two requests). Catches the over-correction where all PUTs are
// made non-retryable.
func TestRetryPolicy_UpdateRetried(t *testing.T) {
	srv, calls := stub500Then200(t, `{"id":1,"name":"wm"}`)
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.UpdateWatermark(context.Background(), 1, WatermarkPayload{Name: "wm"}); err != nil {
		t.Fatalf("UpdateWatermark: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("UpdateWatermark issued %d requests, want 2 — a full-state PUT update MUST retry on transient failures (making doPUT retryable=false for /watermarks/{id} drops this to 1)", got)
	}
}
