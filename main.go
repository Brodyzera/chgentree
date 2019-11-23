package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
)

// Node is a type that contains Organization data as well as a list of references to children Nodes.
type Node struct {
	mux                  sync.Mutex // For locking Children Node array
	BusinessOrganization Organization
	Children             []*Node
}

// Organization is a type that contains an Organizations Name and ID, as well as a list of sub-Organizations.
type Organization struct {
	Name               string
	ID                 string
	SubOrganizationIds []string
	Environments       []*Environment
}

// Environment is a type that contains an Environemnt Name and ID.
type Environment struct {
	ID           string
	Name         string
	Applications []*Application
}

// Application is a type that contains an Application Domain, Full Domain, Status, and File Name.
type Application struct {
	Domain     string
	FullDomain string
	Status     string
	FileName   string
	Workers    struct {
		Type struct {
			CPU string
		} `json:"type"`
		Amount              int
		RemainingOrgWorkers float32
		TotalOrgWorkers     float32
	} `json:"workers"`
	LastUpdateTime int
	MuleVersion    struct {
		Version string
	} `json:"muleVersion"`
}

// To be set my the command line.
var rootID, username, password *string

func errorCheck(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s", err)
		os.Exit(1)
	}
}

// InitTree initializes a new organization heirarchy tree.
func InitTree() *Node {
	g := &sync.WaitGroup{}

	// Construct root Node
	byteArray := getOrganizationMetrics(*rootID)
	var organization Organization
	json.Unmarshal(byteArray, &organization)
	node := &Node{BusinessOrganization: organization, Children: nil}

	// Build remaining Nodes
	g.Add(1)
	node.buildOrgTree(g)
	g.Wait()

	return node
}

func (p *Node) buildOrgTree(g *sync.WaitGroup) {
	defer g.Done()
	for _, v := range p.BusinessOrganization.SubOrganizationIds {
		byteArray := getOrganizationMetrics(v)
		var organization Organization
		json.Unmarshal(byteArray, &organization)

		node := &Node{BusinessOrganization: organization, Children: nil}

		p.mux.Lock()
		p.Children = append(p.Children, node)
		p.mux.Unlock()

		g.Add(1)
		go node.buildOrgTree(g)
	}
}

func getOrganizationMetrics(orgID string) []byte {
	const organizationsEndpoint string = "https://anypoint.mulesoft.com/accounts/api/organizations/"
	requestURL := fmt.Sprintf("%s%s", organizationsEndpoint, orgID)

	client := &http.Client{}

	req, err := http.NewRequest("GET", requestURL, nil)
	errorCheck(err)
	req.SetBasicAuth(*username, *password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	errorCheck(err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("Non-OK HTTP status:", resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	errorCheck(err)

	return body
}

func getDeployedArtifacts(environment string) []byte {
	const organizationsEndpoint string = "https://anypoint.mulesoft.com/cloudhub/api/v2/applications"

	client := &http.Client{}

	req, err := http.NewRequest("GET", organizationsEndpoint, nil)
	errorCheck(err)
	req.SetBasicAuth(*username, *password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-anypnt-env-id", environment)

	resp, err := client.Do(req)
	errorCheck(err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("Non-OK HTTP status:", resp.StatusCode)
		os.Exit(1)
	}

	body, err := ioutil.ReadAll(resp.Body)
	errorCheck(err)

	return body
}

func searchForArtifact(p *Node, domain string, g *sync.WaitGroup) {
	defer g.Done()
	// First check target node for deployed artifact
	for _, environment := range p.BusinessOrganization.Environments {
		byteArray := getDeployedArtifacts(environment.ID)
		var applications []Application
		json.Unmarshal(byteArray, &applications)

		for _, app := range applications {
			if app.Domain == domain {
				fmt.Println("FOUND IT")
				fmt.Printf("%s", string(byteArray))
			}
		}
	}

	for _, v := range p.Children {
		g.Add(1)
		go searchForArtifact(v, domain, g)
	}
}

func generateApplications(p *Node, g *sync.WaitGroup) {
	defer g.Done()
	for _, environment := range p.BusinessOrganization.Environments {
		byteArray := getDeployedArtifacts(environment.ID)
		var applications []*Application
		json.Unmarshal(byteArray, &applications)

		environment.Applications = applications
	}

	for _, c := range p.Children {
		g.Add(1)
		go generateApplications(c, g)
	}
}

func flattenTree(p *Node, orgMap map[string]Organization) {
	orgMap[p.BusinessOrganization.Name] = p.BusinessOrganization

	for _, c := range p.Children {
		flattenTree(c, orgMap)
	}
}

func writeMetricsFile(data interface{}, filename string) (int, error) {
	b, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return -1, err
	}

	f, err := os.Create(filename)
	if err != nil {
		return -1, err
	}
	defer f.Close()

	return f.Write(b)
}

func main() {
	g := &sync.WaitGroup{}
	rootID = flag.String("rootid", "", "The ID for the tree's root organization.")
	username = flag.String("username", "", "The username for the Cloudhub account with access to the target Enterprise.")
	password = flag.String("password", "", "The password for the Cloudhub account with access to the target Enterprise.")
	outdir := flag.String("outdir", ".", "The directory to write the output files to.  Defaults to the bin's current directory.")
	flag.Parse()

	if (*rootID == "") || (*username == "") || (*password == "") {
		fmt.Println("You are missing one or more flags.")
		os.Exit(1)
	}

	// Generate Organization hierarchy and write to file
	head := InitTree()

	g.Add(1)
	generateApplications(head, g)
	g.Wait()

	bytes, err := writeMetricsFile(head, *outdir+"/metrics.json")
	errorCheck(err)
	fmt.Printf("wrote %d bytes\n", bytes)

	// Flatten Organization hierarchy and write to file
	orgMap := make(map[string]Organization)
	flattenTree(head, orgMap)
	values := []Organization{}
	for _, value := range orgMap {
		fmt.Println(value)
		values = append(values, value)
	}

	bytes, err = writeMetricsFile(values, *outdir+"/metrics_flat.json")
	errorCheck(err)
	fmt.Printf("wrote %d bytes\n", bytes)
}
