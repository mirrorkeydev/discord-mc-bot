// Discord bot command handlers.
// These receive command arguments, call out to management server
// functions if necessary, and package the response for the bot user.

package handlers

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/mirrorkeydev/discord-mc-bot/server"
	log "github.com/sirupsen/logrus"
)

func Ping(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionApplicationCommandResponseData{
			Content: "pong :ping_pong:",
		},
	})
}

func Server(s *discordgo.Session, i *discordgo.InteractionCreate) {
	content := ""
	switch i.Data.Options[0].Name {
	case "up":
		content = "bringing up the server... "
	case "down":
		content = "bringing down the server (this might take a minute or two)... "
	default:
		content = "something has gone wrong, and you executed a command that doesn't exist. Congrats! :tada:"
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionApplicationCommandResponseData{
			Content: content,
		},
	})

	var success bool
	var res string
	switch i.Data.Options[0].Name {
	case "up":
		success, res = server.BringUpServer()
		if success {
			err := s.UpdateGameStatus(0, fmt.Sprintf("server up @ %v", server.ManagementServerAddress))
			if err != nil {
				log.WithError(err).Error("unable to update status")
			}
		}
	case "down":
		success, res = server.BringDownServer()
		if success {
			err := s.UpdateGameStatus(0, "server down")
			if err != nil {
				log.WithError(err).Error("unable to update status")
			}
		}
	}

	err := s.InteractionResponseEdit(s.State.User.ID, i.Interaction, &discordgo.WebhookEdit{
		Content: content + res,
	})
	if err != nil {
		s.FollowupMessageCreate(s.State.User.ID, i.Interaction, true, &discordgo.WebhookParams{
			Content: "something went wrong",
		})
		return
	}
}

func Whitelist(s *discordgo.Session, i *discordgo.InteractionCreate) {
	playerUsername := i.Data.Options[0].StringValue()
	content := fmt.Sprintf("whitelisting player %v...", playerUsername)

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionApplicationCommandResponseData{
			Content: content,
		},
	})

	var res = ""

	serverIsUp, err := mcServerIsUp(s)
	if err != nil {
		res = "unable to check if MC server is up"
	} else if !serverIsUp {
		res = "the server isn't up, so you can't whitelist players. try starting the server first"
	} else {
		_, res = server.Whitelist(playerUsername)
	}

	err = s.InteractionResponseEdit(s.State.User.ID, i.Interaction, &discordgo.WebhookEdit{
		Content: content + res,
	})
	if err != nil {
		s.FollowupMessageCreate(s.State.User.ID, i.Interaction, true, &discordgo.WebhookParams{
			Content: "something went wrong",
		})
		return
	}
}

func mcServerIsUp(s *discordgo.Session) (bool, error) {
	serverIsUp, err := server.IsUp()
	if err != nil {
		return false, err
	}

	if serverIsUp {
		err = s.UpdateGameStatus(0, fmt.Sprintf("server up @ %v", server.ManagementServerAddress))
		if err != nil {
			log.WithError(err).Error("unable to update status")
		}
		return true, nil
	} else {
		err := s.UpdateGameStatus(0, "server down")
		if err != nil {
			log.WithError(err).Error("unable to update status")
		}
		return false, nil
	}
}
