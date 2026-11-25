package server

import "github.com/johnewart/pyxis/internal/serverutil"

// requireSessionID moved to serverutil.RequireSessionID; kept as an alias so existing
// call sites in this package compile unchanged.
var requireSessionID = serverutil.RequireSessionID
