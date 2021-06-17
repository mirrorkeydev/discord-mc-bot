package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"crypto/tls"
	"crypto/x509"

	"github.com/bwmarrin/discordgo"
	pb "github.com/mirrorkeydev/discord-mc-bot/proto"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
)

// Bot parameters
var (
	RemoveCommands = flag.Bool("rmcmd", true, "Remove all commands after shutdowning or not")
)

var s *discordgo.Session

var Zone string
var GcpProjectId string
var ServerName string
var svc *compute.Service
var managementServerAddress string
var managementServerPort string
var mgcConn *grpc.ClientConn
var mgc pb.MCManagementClient
var DiscordBotToken string
var DiscordGuildID string

func waitForOperation(op *compute.Operation) error {
	for {
		result, err := svc.ZoneOperations.Get(GcpProjectId, Zone, op.Name).Do()
		if err != nil {
			return fmt.Errorf("failed retriving operation status: %s", err)
		}

		if result.Status == "DONE" {
			if result.Error != nil {
				var errors []string
				for _, e := range result.Error.Errors {
					errors = append(errors, e.Message)
				}
				return fmt.Errorf("operation failed with error(s): %s", strings.Join(errors, ", "))
			}
			break
		}
		time.Sleep(time.Second)
	}
	return nil
}

func init() { flag.Parse() }

func init() {
	Zone = "us-west1-b"
	GcpProjectId = "mc-server-316300"
	ServerName = "mc-server"
	managementServerAddress = "garage.prototypical.pro"
	managementServerPort = "50051"
	DiscordBotToken = os.Getenv("DISCORD_BOT_TOKEN")
	if DiscordBotToken == "" {
		log.Fatal("Environment Variable DISCORD_BOT_TOKEN not set.")
	}
	DiscordGuildID = os.Getenv("DISCORD_GUILD_ID")
	if DiscordGuildID == "" {
		log.Fatal("Environment Variable DISCORD_GUILD_ID not set.")
	}
}

func init() {
	var err error
	s, err = discordgo.New("Bot " + DiscordBotToken)
	if err != nil {
		log.Printf("Invalid bot parameters: %v", err)
	}
}


func initializeConnectionToManagementServer() error {
	certificate, err := tls.LoadX509KeyPair(
		"certs/discord-mc-client.crt",
		"certs/discord-mc-client.key",
	)
	if err != nil {
		log.Printf("failed to read ca cert files: %s \n", err)
		return err
	}

	certPool := x509.NewCertPool()
	bs, err := ioutil.ReadFile("certs/discord-mc.crt")
	if err != nil {
		log.Printf("failed to read ca cert: %s \n", err)
		return err
	}

	ok := certPool.AppendCertsFromPEM(bs)
	if !ok {
		log.Printf("failed to append certs \n")
		return err
	}

	transportCreds := credentials.NewTLS(&tls.Config{
		ServerName:   managementServerAddress,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      certPool,
	})

	mgcConn, err = grpc.Dial(fmt.Sprintf("%v:%v", managementServerAddress, managementServerPort), grpc.WithBlock(), grpc.WithTransportCredentials(transportCreds))
	if err != nil {
		log.Fatalf("did not connect: %v /n", err)
		return err
	}

	mgc = pb.NewMCManagementClient(mgcConn)
	log.Println("Connected to MC management server!")
	return nil
}

func bringUpServer() (bool, string) {
	log.Println("bringUpServer():")
	instance, err := svc.Instances.Get(GcpProjectId, Zone, ServerName).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				log.Println("No VM instance available. Creating one now... ")

				instanceOptions := compute.Instance{
					Name:        ServerName,
					Description: "A server used by Houses United to play MC",
					Zone:        Zone,
					MachineType: "zones/us-west1-a/machineTypes/e2-standard-2",
					Disks: []*compute.AttachedDisk{
						{
							AutoDelete: true,
							Boot:       true,
							Type:       "PERSISTENT",
							InitializeParams: &compute.AttachedDiskInitializeParams{
								DiskName:    "my-root-pd",
								SourceImage: "projects/ubuntu-os-cloud/global/images/ubuntu-2004-focal-v20210610",
							},
						},
					},
					NetworkInterfaces: []*compute.NetworkInterface{{}},
				}
				opi, err := svc.Instances.Insert(GcpProjectId, Zone, &instanceOptions).Do()
				if err != nil {
					log.Println("Call to create GCP instance failed. ", err)
					return false, "failed"
				}
				err = waitForOperation(opi)
				if err != nil {
					log.Println("Cannot create GCP instance. ", err)
					return false, "failed"
				}
				log.Printf("Instance id %v created\n", opi.TargetId)
				instance, err = svc.Instances.Get(GcpProjectId, Zone, ServerName).Do()
				if err != nil {
					log.Println("Cannot get instance details. ", err)
					return false, "failed"
				}
			} else {
				log.Println("Cannot get available instances. ", err)
				return false, "failed"
			}
		} else {
			log.Println("Cannot get available instances. ", err)
			return false, "failed"
		}
	}

	for {
		switch instance.Status {
		case "RUNNING":
			log.Println("Instance was already running, doing nothing. ")
			return true, "done!"
		case "STOPPED", "TERMINATED":
			log.Println("Instance was stopped, trying to start it now. ")
			ops, err := svc.Instances.Start(GcpProjectId, Zone, ServerName).Do()
			if err != nil {
				log.Println("Call to start the instance failed. ", err)
				return false, "failed"
			}
			err = waitForOperation(ops)
			if err != nil {
				log.Println("Cannot start GCP instance. ", err)
				return false, "failed"
			}
			log.Println("Instance started!")
			return true, "done! please go do something else for 5 minutes, the server instance is booting up Minecraft"
		case "PROVISIONING", "DEPROVISIONING", "REPAIRING", "STAGING", "STOPPING":
			log.Printf("Instance is in transitional status: %v, waiting 5 seconds and then seeing if anything changes \n", instance.Status)
			time.Sleep(time.Second * 5)
			instance, err = svc.Instances.Get(GcpProjectId, Zone, ServerName).Do()
			if err != nil {
				log.Println("Cannot get instance details. ", err)
				return false, "failed"
			}
		case "SUSPENDED", "SUSPENDING":
			log.Printf("Instance is in broken status: %v.\n", instance.Status)
			return false, "server is suspended <&776313105788829727>"
		}
	}
}

func bringDownServer() (bool, string) {
	log.Println("bringDownServer():")
	instance, err := svc.Instances.Get(GcpProjectId, Zone, ServerName).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				log.Println("Server already doesn't exist.")
				return true, "it already didn't exist"
			} else {
				log.Println("Cannot get available instances. ", err)
				return false, "failed"
			}
		} else {
			log.Println("Cannot get available instances. ", err)
			return false, "failed"
		}
	}

	for {
		switch instance.Status {
		case "RUNNING":
			log.Println("Instance was running, trying to stop it now. ")
			ops, err := svc.Instances.Stop(GcpProjectId, Zone, ServerName).Do()
			if err != nil {
				log.Println("Call to stop the instance failed. ", err)
				return false, "failed"
			}
			err = waitForOperation(ops)
			if err != nil {
				log.Println("Cannot stop GCP instance. ", err)
				return false, "failed"
			}
			log.Println("Instance stopped!")
			return true, "done!"
		case "STOPPED", "TERMINATED":
			log.Println("Instance was already stopped, doing nothing. ")
			return true, "it was already stopped!"
		case "PROVISIONING", "DEPROVISIONING", "REPAIRING", "STAGING", "STOPPING":
			log.Printf("Instance is in transitional status: %v, waiting 5 seconds and then seeing if anything changes \n", instance.Status)
			time.Sleep(time.Second * 5)
			instance, err = svc.Instances.Get(GcpProjectId, Zone, ServerName).Do()
			if err != nil {
				log.Println("Cannot get instance details. ", err)
				return false, "failed"
			}
		case "SUSPENDED", "SUSPENDING":
			log.Printf("Instance is in broken status: %v.\n", instance.Status)
			return false, "server is suspended <&776313105788829727>"
		}
	}
}

func whitelist(user string) (bool, string) {
	log.Println("whitelist():")

	if mgcConn == nil || mgcConn.GetState() != connectivity.Ready {
		err := initializeConnectionToManagementServer()
		if err != nil {
			log.Printf("Unable to connect to management server: %v", err)
			return false, "unable to connect to management server"
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := mgc.UpdateWhitelist(ctx, &pb.UpdateWhitelistRequest{
		Action: pb.UpdateWhitelistRequest_ADD,
		PlayerName: user,
	})
	if err != nil {
		log.Printf("could not whitelist %v: %v", user, err)
		return false, fmt.Sprintf("whitelist operation failed: %v", err)
	}
	if r.ResultCode != pb.UpdateWhitelistResponse_ADD_OK {
		log.Printf("could not whitelist %v: %v", user, r.Response)
		return false, fmt.Sprintf("whitelist operation failed: %v", err)
	}
	return true, "done!"
}

var (
	commands = []*discordgo.ApplicationCommand{
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
	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ping": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionApplicationCommandResponseData{
					Content: "pong :ping_pong:",
				},
			})
		},
		"server": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
				success, res = bringUpServer()
				if success {
					err := s.UpdateGameStatus(0, fmt.Sprintf("server up @ %v", managementServerAddress))
					if err != nil {
						log.Println("Unable to update status. ", err)
					}
				}
			case "down":
				success, res = bringDownServer()
				if success {
					err := s.UpdateGameStatus(0, "server down")
					if err != nil {
						log.Println("Unable to update status. ", err)
					}
				}
			}

			err := s.InteractionResponseEdit(s.State.User.ID, i.Interaction, &discordgo.WebhookEdit{
				Content: content + res,
			})
			if err != nil {
				s.FollowupMessageCreate(s.State.User.ID, i.Interaction, true, &discordgo.WebhookParams{
					Content: "something went wrong. ",
				})
				return
			}
		},
		"whitelist": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			playerUsername := i.Data.Options[0].StringValue()
			content := fmt.Sprintf("whitelisting player %v...", playerUsername)

			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionApplicationCommandResponseData{
					Content: content,
				},
			})

			_, res := whitelist(playerUsername)
			err := s.InteractionResponseEdit(s.State.User.ID, i.Interaction, &discordgo.WebhookEdit{
				Content: content + res,
			})
			if err != nil {
				s.FollowupMessageCreate(s.State.User.ID, i.Interaction, true, &discordgo.WebhookParams{
					Content: "something went wrong. ",
				})
				return
			}
		},
	}
)

func init() {

	privatekey, err := os.ReadFile("./certs/google-private-key.txt")
	if err != nil {
		log.Fatalf("Unable to read google private key from file. %v", err)
	}

	conf := &jwt.Config{
		Email:      os.Getenv("CLIENT_EMAIL"),
		PrivateKey: privatekey,
		Scopes: []string{
			"https://www.googleapis.com/auth/compute",
		},
		TokenURL: google.JWTTokenURL,
	}

	httpClient := conf.Client(context.Background())
	svc, err = compute.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		log.Println("Cannot create the compute service. ", err)
	}
	log.Println("Compute service is ready!")
}

func init() {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.Data.Name]; ok {
			h(s, i)
		}
	})
}

func main() {
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Println("Bot is up!")
	})
	err := s.Open()
	if err != nil {
		log.Panicf("Cannot open the session: %v \n", err)
	}

	existingCommands, err := s.ApplicationCommands(s.State.User.ID, DiscordGuildID)
	if err != nil {
		log.Panic("Cannot fetch all commands. ", err)
	}
	for _, v := range existingCommands {
		err := s.ApplicationCommandDelete(s.State.User.ID, DiscordGuildID, v.ID)
		log.Println("Deleting existing command: ", v.Name)
		if err != nil {
			log.Panicf("Cannot delete '%v' command: %v", v.Name, err)
		}
	}

	for _, v := range commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, DiscordGuildID, v)
		log.Println("Creating command: ", v.Name)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
	}

	log.Println("Commands are ready!")

	defer s.Close()
	defer mgcConn.Close()

	stop := make(chan os.Signal)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Println("Gracefully shutdowning")
}
