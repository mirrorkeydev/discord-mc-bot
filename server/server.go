// Server functionality: directly interacts with GCP compute to
// control the VM instance, or connects to the management server
// in the VM to control the MC instance.

package server

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/mirrorkeydev/discord-mc-bot/proto"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

var gcpClientEmail string
var gcpComputeService *compute.Service
var gcpProjectId string
var gcpServerName string
var gcpZone string

var ManagementServerAddress string
var managementServerClient pb.MCManagementClient
var managementServerConnection *grpc.ClientConn
var managementServerPort string

// Set up variables, loading from environment where necessary
func init() {
	gcpZone = "us-west1-b"
	gcpProjectId = "mc-server-316300"
	gcpServerName = "mc-server"

	ManagementServerAddress = "garage.prototypical.pro"
	managementServerPort = "50051"

	gcpClientEmail = os.Getenv("CLIENT_EMAIL")
	if gcpClientEmail == "" {
		log.Fatal("Environment Variable CLIENT_EMAIL not set.")
	}
}

// Set up connection to GCP compute service
func init() {
	privatekey, err := os.ReadFile("./certs/google-private-key.txt")
	if err != nil {
		log.WithError(err).Fatal("unable to read google private key from file")
	}

	conf := &jwt.Config{
		Email:      gcpClientEmail,
		PrivateKey: privatekey,
		Scopes: []string{
			"https://www.googleapis.com/auth/compute",
		},
		TokenURL: google.JWTTokenURL,
	}

	httpClient := conf.Client(context.Background())
	gcpComputeService, err = compute.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		log.WithError(err).Fatal("cannot create the compute service. ")
	}
	log.Info("Compute service is ready!")
}

func BringUpServer() (bool, string) {
	instance, err := gcpComputeService.Instances.Get(gcpProjectId, gcpZone, gcpServerName).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				log.Info("No VM instance available. Creating one now... ")

				instanceOptions := compute.Instance{
					Name:        gcpServerName,
					Description: "A server used by Houses United to play MC",
					Zone:        gcpZone,
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
				opi, err := gcpComputeService.Instances.Insert(gcpProjectId, gcpZone, &instanceOptions).Do()
				if err != nil {
					log.Info("Call to create GCP instance failed. ", err)
					return false, "failed"
				}
				err = waitForOperation(opi)
				if err != nil {
					log.Info("Cannot create GCP instance. ", err)
					return false, "failed"
				}
				log.Infof("Instance id %v created\n", opi.TargetId)
				instance, err = gcpComputeService.Instances.Get(gcpProjectId, gcpZone, gcpServerName).Do()
				if err != nil {
					log.Info("Cannot get instance details. ", err)
					return false, "failed"
				}
			} else {
				log.Info("Cannot get available instances. ", err)
				return false, "failed"
			}
		} else {
			log.Info("Cannot get available instances. ", err)
			return false, "failed"
		}
	}

	for {
		switch instance.Status {
		case "RUNNING":
			log.Info("Instance was already running, doing nothing. ")
			return true, "instance was already running :clown:"
		case "STOPPED", "TERMINATED":
			log.Info("Instance was stopped, trying to start it now. ")
			ops, err := gcpComputeService.Instances.Start(gcpProjectId, gcpZone, gcpServerName).Do()
			if err != nil {
				log.Info("Call to start the instance failed. ", err)
				return false, "failed"
			}
			err = waitForOperation(ops)
			if err != nil {
				log.Info("Cannot start GCP instance. ", err)
				return false, "failed"
			}
			log.Info("Instance started!")
			return true, "done! please go do something else for 5 minutes, the server instance is booting up Minecraft"
		case "PROVISIONING", "DEPROVISIONING", "REPAIRING", "STAGING", "STOPPING":
			log.Infof("Instance is in transitional status: %v, waiting 5 seconds and then seeing if anything changes \n", instance.Status)
			time.Sleep(time.Second * 5)
			instance, err = gcpComputeService.Instances.Get(gcpProjectId, gcpZone, gcpServerName).Do()
			if err != nil {
				log.Info("Cannot get instance details. ", err)
				return false, "failed"
			}
		case "SUSPENDED", "SUSPENDING":
			log.Infof("Instance is in suspended (sleep) status: %v.\n", instance.Status)
			return false, "server is suspended <&776313105788829727>"
		}
	}
}

func BringDownServer() (bool, string) {
	instance, err := gcpComputeService.Instances.Get(gcpProjectId, gcpZone, gcpServerName).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				log.Info("Server already doesn't exist.")
				return true, "it already didn't exist"
			} else {
				log.Info("Cannot get available instances. ", err)
				return false, "failed"
			}
		} else {
			log.Info("Cannot get available instances. ", err)
			return false, "failed"
		}
	}

	for {
		switch instance.Status {
		case "RUNNING":
			log.Info("Instance was running, trying to stop it now. ")

			if managementServerConnection != nil {
				managementServerConnection.Close()
			}

			ops, err := gcpComputeService.Instances.Stop(gcpProjectId, gcpZone, gcpServerName).Do()
			if err != nil {
				log.Info("Call to stop the instance failed. ", err)
				return false, "failed"
			}
			err = waitForOperation(ops)
			if err != nil {
				log.Info("Cannot stop GCP instance. ", err)
				return false, "failed"
			}
			log.Info("Instance stopped!")
			return true, "done!"
		case "STOPPED", "TERMINATED":
			log.Info("Instance was already stopped, doing nothing. ")
			return true, "it was already stopped!"
		case "PROVISIONING", "DEPROVISIONING", "REPAIRING", "STAGING", "STOPPING":
			log.Infof("Instance is in transitional status: %v, waiting 5 seconds and then seeing if anything changes \n", instance.Status)
			time.Sleep(time.Second * 5)
			instance, err = gcpComputeService.Instances.Get(gcpProjectId, gcpZone, gcpServerName).Do()
			if err != nil {
				log.Info("Cannot get instance details. ", err)
				return false, "failed"
			}
		case "SUSPENDED", "SUSPENDING":
			log.Info("Instance is in suspended (sleep) status: %v.\n", instance.Status)
			return false, "server is suspended <&776313105788829727>"
		}
	}
}

func Whitelist(user string) (bool, string) {
	if managementServerConnection == nil || managementServerConnection.GetState() != connectivity.Ready {
		err := initiateConnectionToManagementServer()
		if err != nil {
			log.Infof("Unable to connect to management server: %v", err)
			return false, "unable to connect to management server"
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, err := managementServerClient.UpdateWhitelist(ctx, &pb.UpdateWhitelistRequest{
		Action:     pb.UpdateWhitelistRequest_ADD,
		PlayerName: user,
	})
	if err != nil {
		log.Infof("could not whitelist %v: %v", user, err)
		return false, fmt.Sprintf("whitelist operation failed: %v", err)
	}
	if r.ResultCode != pb.UpdateWhitelistResponse_ADD_OK {
		log.Infof("could not whitelist %v: %v", user, r.Response)
		return false, fmt.Sprintf("whitelist operation failed: %v", r.Response)
	}
	return true, "done!"
}
