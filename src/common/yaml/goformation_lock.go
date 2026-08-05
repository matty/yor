package yaml

import "sync"

// GoformationLock serialises every call that reaches goformation's intrinsics
// processing, from any parser.
//
// intrinsics.ProcessYAML re-registers goformation's custom YAML tag unmarshalers on
// every call, and those are held in a package level map inside sanathkr/go-yaml. The
// CloudFormation parser reaches it through goformation.OpenWithOptions and the
// Serverless parser through intrinsics.ProcessYAML directly, so a lock owned by
// either package on its own protects nothing: with YOR_WORKER_NUM above 1 a template
// parse and a serverless parse land on that map together. Both callers have to take
// the same lock, which is why it lives here rather than next to either parser.
var GoformationLock sync.Mutex
