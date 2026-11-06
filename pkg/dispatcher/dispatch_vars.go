package dispatcher

// dispatchConfigFor picks the var bindings a card dispatched to routeKey (the
// bot's name: iss.Bot, else iss.Assignee) runs with. First match wins:
//
//  1. an explicit assignee_dispatch entry of the operator's config — found by
//     the same tolerant name routing uses (lookupBotKey), so an entry for
//     `my_helper` serves a card assigned `My-Helper`;
//  2. the dispatch_vars of the bot's manifest, read from the catalogue at
//     dispatch time (RoutingRunner.BotDispatchVars): a bot created or edited
//     after the dispatcher was configured gets its inputs without anyone
//     regenerating the config;
//  3. the global dispatch.vars.
//
// The bot's own vars REPLACE the global ones wholesale (never merged), as an
// assignee_dispatch entry always has.
func (c *Dispatcher) dispatchConfigFor(cfg *Config, routeKey string) DispatchConfig {
	if routeKey == "" {
		return cfg.Dispatch
	}
	if ov, ok := lookupBotKey(cfg.AssigneeDispatch, routeKey); ok {
		return ov
	}
	if bv, ok := c.runner.(interface {
		BotDispatchVars(string) (map[string]string, bool)
	}); ok {
		if vars, found := bv.BotDispatchVars(routeKey); found && len(vars) > 0 {
			return DispatchConfig{Vars: vars}
		}
	}
	return cfg.Dispatch
}
