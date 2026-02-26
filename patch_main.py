import sys

with open("main.go", "r") as f:
    content = f.read()

init_obsolete = """	// Initialize Conspiribot once if configured
	for _, v := range config.Ircd {
		if len(v.Conspiribot.Bots) > 0 {
			_, err := conspiribot.Init(context.Background(), "swarm.db", v.Apikey, &v.Conspiribot)
			if err != nil {
				log.Printf("Failed to initialize Conspiribot: %v", err)
			}
			break
		}
	}"""
content = content.replace(init_obsolete, "")

connect_obsolete = """	if appConfig.DatabaseAddress != "" {
		context, cancel := context.WithTimeout(context.Background(), time.Duration(appConfig.RequestTimeout)*time.Second)
		defer cancel()

		go connectToDB(&appConfig, &context, irc)
	}"""
connect_new = """	if appConfig.DatabaseAddress != "" {
		dbCtx, cancel := context.WithTimeout(context.Background(), time.Duration(appConfig.RequestTimeout)*time.Second)
		defer cancel()

		go func() {
			connectToDB(&appConfig, &dbCtx, irc)
			if len(appConfig.Conspiribot.Bots) > 0 && appConfig.pool != nil {
				_, err := conspiribot.Init(context.Background(), appConfig.pool, appConfig.Apikey, &appConfig.Conspiribot)
				if err != nil {
					log.Printf("Failed to initialize Conspiribot: %v", err)
				}
			}
		}()
	}"""
content = content.replace(connect_obsolete, connect_new)

with open("main.go", "w") as f:
    f.write(content)
