package main

import (
	"os"
	"os/signal"

	log "github.com/sirupsen/logrus"

	"github.com/bwmarrin/discordgo"
	"github.com/mirrorkeydev/discord-mc-bot/handlers"
)

var discordBotToken string
var discordGuildID string
var discordSession *discordgo.Session

func init() {
	discordBotToken = os.Getenv("DISCORD_BOT_TOKEN")
	if discordBotToken == "" {
		log.Fatal("Environment Variable DISCORD_BOT_TOKEN not set.")
	}
	discordGuildID = os.Getenv("DISCORD_GUILD_ID")
	if discordGuildID == "" {
		log.Fatal("Environment Variable DISCORD_GUILD_ID not set.")
	}

	var err error
	discordSession, err = discordgo.New("Bot " + discordBotToken)
	if err != nil {
		log.WithError(err).Fatal("invalid discord bot parameters")
	}

	discordSession.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.Data.Name]; ok {
			h(s, i)
		}
	})
}

var commands = []*discordgo.ApplicationCommand{
	{
		Name:        "ping",
		Description: "Pings the discord bot. ",
	},
	{
		Name:        "server",
		Description: "Control the Minecraft Server",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "up",
				Description: "Bring the server up",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "down",
				Description: "Bring the server down",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	},
	{
		Name:        "whitelist",
		Description: "Whitelist a Minecraft player",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "user",
				Description: "The username of the Minecraft player to whitelist",
				Required:    true,
			},
		},
	},
}

var commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
	"ping":      handlers.Ping,
	"server":    handlers.Server,
	"whitelist": handlers.Whitelist,
}

func setUpCommands() {
	existingGlobalCommands, err := discordSession.ApplicationCommands(discordSession.State.User.ID, "")
	if err != nil {
		log.Fatal("cannot fetch all global commands. ")
	}
	for _, v := range existingGlobalCommands {
		err := discordSession.ApplicationCommandDelete(discordSession.State.User.ID, "", v.ID)
		log.Info("deleting existing global command: ", v.Name)
		if err != nil {
			log.Fatalf("cannot delete '%v' global command: %v", v.Name, err)
		}
	}

	existingCommands, err := discordSession.ApplicationCommands(discordSession.State.User.ID, discordGuildID)
	if err != nil {
		log.Panic("cannot fetch all commands. ")
	}
	for _, v := range existingCommands {
		err := discordSession.ApplicationCommandDelete(discordSession.State.User.ID, discordGuildID, v.ID)
		log.Info("deleting existing command: ", v.Name)
		if err != nil {
			log.Panicf("cannot delete '%v' command: %v", v.Name, err)
		}
	}

	for _, v := range commands {
		_, err := discordSession.ApplicationCommandCreate(discordSession.State.User.ID, discordGuildID, v)
		log.Info("creating command: ", v.Name)
		if err != nil {
			log.Fatalf("cannot create '%v' command: %v", v.Name, err)
		}
	}

	log.Info("Commands are ready!")
}

func main() {
	discordSession.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Info("Bot is up!")
	})

	err := discordSession.Open()
	if err != nil {
		log.WithError(err).Panic("cannot open the session")
	}

	setUpCommands()

	defer discordSession.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Info("Gracefully shutdowning")
}
