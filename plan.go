package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"

	"golang.org/x/net/context"

	"github.com/docker/docker/api/client/bundlefile"
	"github.com/docker/docker/api/client/stack"
	"github.com/docker/engine-api/client"
	"github.com/docker/engine-api/types"
	"github.com/docker/engine-api/types/filters"
	"github.com/docker/engine-api/types/swarm"
	"github.com/fatih/color"
	"github.com/urfave/cli"
)

var Replica1 uint64 = 1

type Services map[string]swarm.Service

func plan(c *cli.Context) error {
	stackName := c.Args().Get(0)

	if stackName == "" {
		return cli.NewExitError("Need to specify a stack name", 1)
	}
	dabLocation := c.String("dab")

	if dabLocation == "" {
		// Assume it is called as the stack name
		dabLocation = fmt.Sprintf("%s.dab", stackName)
	}

	var dabReader io.Reader
	if u, e := url.Parse(dabLocation); e == nil && u.IsAbs() {
		// DAB file seems to be remote, try to download it first
		return cli.NewExitError("Not implemented", 2)
	} else {
		if dabFile, err := os.Open(dabLocation); err != nil {
			return cli.NewExitError(err.Error(), 3)
		} else {
			dabReader = dabFile
		}
	}

	bundle, bundleErr := bundlefile.LoadFile(dabReader)
	if bundleErr != nil {
		return cli.NewExitError(bundleErr.Error(), 3)
	}

	detail := c.Bool("detail")
	target := c.StringSlice("target")
	targetMap := map[string]bool{}

	for _, name := range target {
		targetMap[name] = true
	}

	swarm, swarmErr := client.NewEnvClient()
	if swarmErr != nil {
		return cli.NewExitError(swarmErr.Error(), 3)
	}

	filter := filters.NewArgs()
	filter.Add("label", "com.docker.stack.namespace="+stackName)
	services, servicesErr := swarm.ServiceList(context.Background(), types.ServiceListOptions{Filter: filter})
	if servicesErr != nil {
		return cli.NewExitError(servicesErr.Error(), 3)
	}

	expected := getBundleServicesSpec(bundle, stackName)
	translateNetworkToIds(&expected, swarm, stackName)

	current := getSwarmServicesSpecForStack(services)

	w := bufio.NewWriter(os.Stdout)
	sp := NewServicePrinter(w, detail)

	for n, es := range expected {
		// Only process found target services
		if _, found := targetMap[es.Spec.Name]; len(targetMap) == 0 || found {
			if cs, found := current[n]; !found {
				// New service to create
				color.Green("\n+ %s", n)
				sp.PrintServiceSpec(es.Spec)
				w.Flush()
			} else {
				different := sp.PrintServiceSpecDiff(cs.Spec, es.Spec)
				if different {
					color.Yellow("\n~ %s\n", es.Spec.Name)
				} else if detail {
					color.Cyan("\n%s\n", es.Spec.Name)
				}
				w.Flush()
			}
		}
	}

	// Checks services to remove
	for n, cs := range current {
		// Only process found target services
		if _, found := targetMap[cs.Spec.Name]; len(targetMap) == 0 || found {
			if _, found := expected[n]; !found {
				color.Red("\n- %s", n)
				sp.PrintServiceSpec(cs.Spec)
				w.Flush()
			}
		}

	}

	return nil
}

func safeDereference(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func translateNetworkToIds(services *Services, cli *client.Client, stackName string) {
	existingNetworks, err := stack.GetNetworks(context.Background(), cli, stackName)
	if err != nil {
		log.Fatal("Error retrieving networks")
	}

	for _, service := range *services {
		for i, network := range service.Spec.Networks {
			for _, enet := range existingNetworks {
				if enet.Name == network.Target {
					service.Spec.Networks[i].Target = enet.ID
					network.Target = enet.ID
				}
			}
		}
	}
}

func getBundleServicesSpec(bundle *bundlefile.Bundlefile, stackName string) Services {
	specs := Services{}

	for name, service := range bundle.Services {
		spec := swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: swarm.ContainerSpec{
					Image:   service.Image,
					Labels:  service.Labels,
					Command: service.Command,
					Args:    service.Args,
					Env:     service.Env,
					Dir:     safeDereference(service.WorkingDir),
					User:    safeDereference(service.User),
				},
				Placement: &swarm.Placement{Constraints: service.Constraints},
			},
			Mode: swarm.ServiceMode{
				Replicated: &swarm.ReplicatedService{
					Replicas: service.Replicas,
				},
			},
			Networks: convertNetworks(service.Networks, stackName, name),
		}

		spec.Mode = swarm.ServiceMode{
			Replicated: &swarm.ReplicatedService{
				Replicas: &Replica1,
			},
		}

		if service.Replicas != nil {
			spec.Mode.Replicated.Replicas = service.Replicas
		}

		spec.Labels = map[string]string{"com.docker.stack.namespace": stackName}
		spec.Name = fmt.Sprintf("%s_%s", stackName, name)

		// Populate ports
		ports := []swarm.PortConfig{}
		for _, port := range service.Ports {
			p := swarm.PortConfig{
				TargetPort: port.Port,
				Protocol:   swarm.PortConfigProtocol(port.Protocol),
			}

			ports = append(ports, p)
		}
		if len(ports) > 0 {
			// Hardcode resolution mode to VIP as it's the default with dab
			spec.EndpointSpec = &swarm.EndpointSpec{Ports: ports, Mode: swarm.ResolutionMode("vip")}
		}

		service := swarm.Service{}
		service.ID = spec.Name
		service.Spec = spec

		specs[spec.Name] = service
	}
	return specs
}

func convertNetworks(networks []string, namespace string, name string) []swarm.NetworkAttachmentConfig {
	nets := []swarm.NetworkAttachmentConfig{}
	for _, network := range networks {
		nets = append(nets, swarm.NetworkAttachmentConfig{
			Target:  namespace + "_" + network,
			Aliases: []string{name},
		})
	}
	return nets
}

func getSwarmServicesSpecForStack(services []swarm.Service) Services {
	specs := Services{}

	for _, service := range services {
		specs[service.Spec.Name] = service
	}

	return specs
}
