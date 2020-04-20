package main

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

const (
	VPCcidr string = "10.0.0.0/16"
	SUBcidr string = "10.0.1.0/24"
	SGname  string = "my-prod-sg-web-01"
	keyPair string = "my-key-pair"
	region  string = "us-east-2"
	sgDesc  string = "My test SG"
	envk    string = "Environment"
	envv    string = "Non-Prod"
	insT    string = "InstanceType"
	ami     string = "amzn2-ami-hvm-2.0.*"
)

func main() {
	now := time.Now()
	cfg, err := external.LoadDefaultAWSConfig(
		external.WithDefaultRegion(region),
	)
	if err != nil {
		fmt.Println(err)
	}
	vpcch := make(chan ec2.CreateVpcResponse)
	sgch := make(chan *ec2.CreateSecurityGroupResponse)
	azch := make(chan []ec2.AvailabilityZone)
	sbch := make(chan *ec2.Subnet)
	amID := make(chan string)
	keyp := make(chan string)
	ec2c := make(chan *ec2.RunInstancesResponse)
	go getAZs(cfg, azch)
	go createVPC(VPCcidr, cfg, vpcch)
	vpcid := <-vpcch
	go createSG(SGname, *vpcid.Vpc.VpcId, cfg, sgch)
	go createSubnet(vpcid.Vpc.VpcId, cfg, <-azch, SUBcidr, sbch)
	go getAMI(cfg, amID)
	go createKey(cfg, keyp)
	sgs := <-sgch
	sub := <-sbch
	go createEC2(cfg, *sub.SubnetId, <-amID, <-keyp, *sgs.GroupId, ec2c)
	fmt.Printf("Instance created: %v\n", (<-ec2c).Instances)
	diff := time.Now().Sub(now).Seconds()
	fmt.Printf("time took: %.2f seconds\n", diff)
}

func createSG(sgn, vpch string, cfg aws.Config, ch chan *ec2.CreateSecurityGroupResponse) {

	input := &ec2.CreateSecurityGroupInput{
		Description: aws.String(),
		GroupName:   aws.String(sgn),
		VpcId:       aws.String(vpch),
	}
	SG := ec2.New(cfg)
	req := SG.CreateSecurityGroupRequest(input)
	res, err := req.Send(context.Background())
	if err != nil {
		panic(err)
	}
	ch <- res
}

func createVPC(block string, cfg aws.Config, ch chan ec2.CreateVpcResponse) {
	input := &ec2.CreateVpcInput{
		CidrBlock: aws.String(block),
	}
	VPC := ec2.New(cfg)
	req := VPC.CreateVpcRequest(input)
	res, err := req.Send(context.TODO())
	if err != nil {
		panic(err)
	}
	ch <- *res
}

func createSubnet(vpc *string, cfg aws.Config, az []ec2.AvailabilityZone, cidr string, ch chan *ec2.Subnet) {
	sub := ec2.New(cfg)
	input := &ec2.CreateSubnetInput{
		AvailabilityZone: az[rand.Intn(len(az)-1)].ZoneName,
		VpcId:            vpc,
		CidrBlock:        aws.String(cidr),
	}
	req := sub.CreateSubnetRequest(input)
	res, err := req.Send(context.Background())
	if err != nil {
		panic(err)
	}
	ch <- res.Subnet
}

func getAZs(cfg aws.Config, ch chan []ec2.AvailabilityZone) {
	az := ec2.New(cfg)
	input := &ec2.DescribeAvailabilityZonesInput{}
	req := az.DescribeAvailabilityZonesRequest(input)
	res, err := req.Send(context.TODO())
	if err != nil {
		panic(err)
	}
	ch <- res.AvailabilityZones
}

func createKey(cfg aws.Config, ch chan string) {
	key := ec2.New(cfg)
	input := &ec2.CreateKeyPairInput{
		KeyName: aws.String(keyPair),
	}
	req := key.CreateKeyPairRequest(input)
	res, err := req.Send(context.Background())
	if err != nil {
		panic(err)
	}
	ch <- *res.KeyName
}

func createEC2(cfg aws.Config, sub string, imageID, key, sgID string, ch chan *ec2.RunInstancesResponse) {
	Ec2 := ec2.New(cfg)
	input := &ec2.RunInstancesInput{
		ImageId:          aws.String(imageID),
		InstanceType:     ec2.InstanceTypeT2Micro,
		KeyName:          aws.String(key),
		SecurityGroupIds: []string{sgID},
		SubnetId:         aws.String(sub),
		MaxCount:         aws.Int64(1),
		MinCount:         aws.Int64(1),
		TagSpecifications: []ec2.TagSpecification{
			{
				ResourceType: ec2.ResourceTypeInstance,
				Tags: []ec2.Tag{
					{Key: aws.String(envk),
						Value: aws.String(envv),
					},
					{Key: aws.String(insT),
						Value: aws.String(ec2.InstanceTypeT2Micro),
					},
				},
			},
		},
	}
	req := Ec2.RunInstancesRequest(input)
	res, err := req.Send(context.Background())
	if err != nil {
		panic(err)
	}
	ch <- res
}

type awsAMI struct {
	ID           string
	CreationTime time.Time
}

type awsAMIs []*awsAMI

func (a awsAMIs) Len() int           { return len(a) }
func (a awsAMIs) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a awsAMIs) Less(i, j int) bool { return a[i].CreationTime.After(a[j].CreationTime) }

func getAMI(cfg aws.Config, ch chan string) {
	ami := ec2.New(cfg)
	input := &ec2.DescribeImagesInput{
		Filters: []ec2.Filter{
			{
				Name:   aws.String("name"),
				Values: []string{ami},
			},
		},
	}
	req := ami.DescribeImagesRequest(input)
	amis, err := req.Send(context.Background())
	if err != nil {
		panic(err)
	}
	amiToSort := make([]*awsAMI, 0)
	for _, ami := range amis.Images {
		amiCreationTime, err := time.Parse(time.RFC3339, *ami.CreationDate)
		if err != nil {
			panic(err)
		}
		if len(ami.ProductCodes) > 0 {
			continue
		}
		amiToSort = append(amiToSort, &awsAMI{
			ID:           *ami.ImageId,
			CreationTime: amiCreationTime,
		})
	}
	sort.Sort(awsAMIs(amiToSort))
	ch <- amiToSort[0].ID
}
