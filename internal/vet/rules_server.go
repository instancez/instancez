package vet

func ruleCORS(c *ctx) {
	for i, o := range c.cfg.Server.CORS.Origins {
		switch o {
		case "*":
			c.add("cors-wildcard", Low, []any{"server", "cors", "origins", i}, "CORS allows every origin",
				"Any website can call this API from a browser.",
				"List only your own site origins in server.cors.origins.")
		case "null":
			c.add("cors-null-origin", Medium, []any{"server", "cors", "origins", i}, "CORS allows the null origin",
				"Sandboxed iframes and local files send the null origin, so attackers can use them to call this API.",
				"Remove \"null\" from server.cors.origins.")
		}
	}
}

func ruleMaxLimit(c *ctx) {
	if c.cfg.Server.MaxLimit == -1 {
		c.add("max-limit-disabled", Medium, []any{"server", "max_limit"}, "Row limit is off",
			"One request can pull a whole table, which makes scraping and memory spikes easy.",
			"Set server.max_limit to a number such as 1000.")
	}
}
