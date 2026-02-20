package main

import (
	"crypto/tls"
	"net/http"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

// listBuckets connects to the S3-compatible endpoint and returns bucket names.
func listBuckets(endpoint, accessKey, secretKey string, skipSSL bool) ([]string, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if skipSSL {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	sess, err := session.NewSession(&aws.Config{
		Endpoint:         aws.String(endpoint),
		Region:           aws.String("us-east-1"),
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
		S3ForcePathStyle: aws.Bool(true),
		HTTPClient:       &http.Client{Transport: tr},
	})
	if err != nil {
		return nil, err
	}

	svc := s3.New(sess)
	out, err := svc.ListBuckets(&s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}

	var names []string
	for _, b := range out.Buckets {
		if b.Name != nil {
			names = append(names, *b.Name)
		}
	}
	return names, nil
}
