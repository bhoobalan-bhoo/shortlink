// Package store is the DynamoDB-backed persistence layer for short links and
// their click logs.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/bhoobalan-bhoo/shortlink/internal/shortid"
)

const (
	slugLen      = 7
	maxCollision = 5
	hashIndex    = "urlHash-index"
)

// ErrNotFound is returned when a slug or URL has no stored link.
var ErrNotFound = errors.New("link not found")

// ErrSlugTaken is returned when a requested custom path is already in use.
var ErrSlugTaken = errors.New("custom path already taken")

// Link is one stored short link.
type Link struct {
	Slug      string `dynamodbav:"slug"`
	LongURL   string `dynamodbav:"longUrl"`
	URLHash   string `dynamodbav:"urlHash"`
	CreatedAt int64  `dynamodbav:"createdAt"`
	Clicks    int64  `dynamodbav:"clicks"`
	ExpiresAt int64  `dynamodbav:"expiresAt,omitempty"`
}

// ClickEvent is one recorded visit to a short link.
type ClickEvent struct {
	Slug      string  `dynamodbav:"slug"`
	SK        string  `dynamodbav:"sk"`
	IP        string  `dynamodbav:"ip"`
	City      string  `dynamodbav:"city,omitempty"`
	Region    string  `dynamodbav:"region,omitempty"`
	Country   string  `dynamodbav:"country,omitempty"`
	CC        string  `dynamodbav:"cc,omitempty"`
	Lat       float64 `dynamodbav:"lat,omitempty"`
	Lon       float64 `dynamodbav:"lon,omitempty"`
	Resolved  bool    `dynamodbav:"resolved"`
	UserAgent string  `dynamodbav:"ua,omitempty"`
	TS        int64   `dynamodbav:"ts"`
}

// Store wraps a DynamoDB client bound to the links and clicks tables.
type Store struct {
	db          *dynamodb.Client
	table       string
	clicksTable string
}

// New returns a Store backed by the given client and table names.
func New(db *dynamodb.Client, table, clicksTable string) *Store {
	return &Store{db: db, table: table, clicksTable: clicksTable}
}

func hashURL(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])
}

// Create stores a short link for longURL.
//   - If custom != "", that exact slug is used (or ErrSlugTaken).
//   - Otherwise, an identical URL shortened before is reused (dedupe via GSI),
//     and a random slug is generated when it's new.
//
// When ttl > 0 the link auto-expires after that duration (DynamoDB TTL).
func (s *Store) Create(ctx context.Context, longURL, custom string, ttl time.Duration) (Link, error) {
	urlHash := hashURL(longURL)

	link := Link{
		LongURL:   longURL,
		URLHash:   urlHash,
		CreatedAt: time.Now().Unix(),
		Clicks:    0,
	}
	if ttl > 0 {
		link.ExpiresAt = time.Now().Add(ttl).Unix()
	}

	if custom != "" {
		link.Slug = custom
		if err := s.put(ctx, link); err != nil {
			var ccf *types.ConditionalCheckFailedException
			if errors.As(err, &ccf) {
				return Link{}, ErrSlugTaken
			}
			return Link{}, err
		}
		return link, nil
	}

	// Dedupe: was this exact URL already shortened?
	existing, err := s.findByHash(ctx, urlHash)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Link{}, err
	}

	// Generate a random slug, retrying on the rare collision.
	for i := 0; i < maxCollision; i++ {
		slug, err := shortid.New(slugLen)
		if err != nil {
			return Link{}, err
		}
		link.Slug = slug
		if err := s.put(ctx, link); err == nil {
			return link, nil
		} else {
			var ccf *types.ConditionalCheckFailedException
			if errors.As(err, &ccf) {
				continue // slug taken — try another
			}
			return Link{}, err
		}
	}
	return Link{}, fmt.Errorf("could not generate a unique slug after %d tries", maxCollision)
}

// put writes a link only if the slug is free.
func (s *Store) put(ctx context.Context, link Link) error {
	item, err := attributevalue.MarshalMap(link)
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(slug)"),
	})
	return err
}

// Get returns the link for a slug, or ErrNotFound.
func (s *Store) Get(ctx context.Context, slug string) (Link, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			"slug": &types.AttributeValueMemberS{Value: slug},
		},
	})
	if err != nil {
		return Link{}, err
	}
	if out.Item == nil {
		return Link{}, ErrNotFound
	}
	var link Link
	if err := attributevalue.UnmarshalMap(out.Item, &link); err != nil {
		return Link{}, err
	}
	return link, nil
}

// IncrementClicks atomically bumps the click counter for a slug.
func (s *Store) IncrementClicks(ctx context.Context, slug string) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			"slug": &types.AttributeValueMemberS{Value: slug},
		},
		UpdateExpression: aws.String("ADD clicks :one"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": &types.AttributeValueMemberN{Value: "1"},
		},
	})
	return err
}

// LogClick records one visit. If SK is empty it is derived from TS so events
// sort chronologically within a slug's partition.
func (s *Store) LogClick(ctx context.Context, e ClickEvent) error {
	if e.SK == "" {
		suffix, _ := shortid.New(5)
		e.SK = fmt.Sprintf("%013d#%s", e.TS, suffix)
	}
	item, err := attributevalue.MarshalMap(e)
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.clicksTable),
		Item:      item,
	})
	return err
}

// ListClicks returns up to limit recent clicks for a slug, newest first.
func (s *Store) ListClicks(ctx context.Context, slug string, limit int32) ([]ClickEvent, error) {
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.clicksTable),
		KeyConditionExpression: aws.String("slug = :s"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": &types.AttributeValueMemberS{Value: slug},
		},
		ScanIndexForward: aws.Bool(false), // newest first
		Limit:            aws.Int32(limit),
	})
	if err != nil {
		return nil, err
	}
	var events []ClickEvent
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// ListLinks returns recent links, newest first. Uses a Scan (one page, ~1MB),
// which is fine for a personal-scale tool; add pagination if it grows.
func (s *Store) ListLinks(ctx context.Context, limit int) ([]Link, error) {
	out, err := s.db.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(s.table),
	})
	if err != nil {
		return nil, err
	}
	var links []Link
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &links); err != nil {
		return nil, err
	}
	sort.Slice(links, func(i, j int) bool {
		return links[i].CreatedAt > links[j].CreatedAt
	})
	if limit > 0 && len(links) > limit {
		links = links[:limit]
	}
	return links, nil
}

// findByHash looks up a link by its URL hash via the GSI.
func (s *Store) findByHash(ctx context.Context, urlHash string) (Link, error) {
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		IndexName:              aws.String(hashIndex),
		KeyConditionExpression: aws.String("urlHash = :h"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":h": &types.AttributeValueMemberS{Value: urlHash},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return Link{}, err
	}
	if len(out.Items) == 0 {
		return Link{}, ErrNotFound
	}
	var link Link
	if err := attributevalue.UnmarshalMap(out.Items[0], &link); err != nil {
		return Link{}, err
	}
	return link, nil
}
