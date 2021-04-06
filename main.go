package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"github.com/gosimple/slug"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"
)

var singleTemplate *template.Template
var albumTemplate *template.Template

var outputRoot string

var httpClient http.Client
var redditClient RedditClient
var imgurClient ImgurClient

var skipDuplicates bool
var skipDuplicatesInAlbums bool
var noAlbums bool

var knownUrls = make(map[string]struct{})
var knownHashes = make(map[string]struct{})

var quiet bool
var overwrite bool
var nsfw bool

var minWidth int
var maxWidth int
var minHeight int
var maxHeight int

var noPortrait bool
var noLandscape bool
var noSquare bool

var parseImages bool

var minSize int
var maxSize int

var allowTypes = make(map[string]struct{})

var throttler *time.Ticker

func main() {
	defaultSingleTemplateStr := `{{.Submission.Subreddit}}/{{.Timestamp}}-{{.Submission.Id}}-{{.Submission.Title | slugify}}{{.Ext}}`
	defaultAlbumTemplateStr := `{{.Submission.Subreddit}}/{{.Timestamp}}-{{.Submission.Id}}-{{.Submission.Title | slugify}}/{{.Num}}-{{.Image.Hash}}{{.Ext}}`

	singleTemplateStr := flag.String("single-template", defaultSingleTemplateStr, "template for image paths, use go template syntax")
	albumTemplateStr := flag.String("album-template", defaultAlbumTemplateStr, "template for image paths in albums, use go template syntax")
	flag.StringVar(&outputRoot, "out", ".", "root output directory")
	flag.BoolVar(&noAlbums, "no-albums", false, "don't download albums")
	flag.BoolVar(&skipDuplicates, "skip-duplicates", true, "skip duplicate single images")
	flag.BoolVar(&skipDuplicatesInAlbums, "skip-duplicates-in-albums", false, "skip duplicate images within imgur albums")
	throttle := flag.Duration("throttle", 2*time.Second, "wait at least this long between requests to the reddit api")
	pageSize := flag.Uint("page-size", 25, "reddit api listing page size")
	search := flag.String("search", "", "search string")
	orientation := flag.String("orientation", "all", "image orientation (landscape|portrait|square|all), separate multiple values with comma")
	minWidthOpt := flag.Uint("min-width", 0, "minimum width")
	minHeightOpt := flag.Uint("min-height", 0, "minimum height")
	maxWidthOpt := flag.Uint("max-width", 0, "maximum width (0 = off)")
	maxHeightOpt := flag.Uint("max-height", 0, "maximum height (0 = off)")
	minScore := flag.Int("min-score", 0, "ignore submissions below this score")
	flag.BoolVar(&quiet, "quiet", false, "don't print every submission (errors and skips are still printed)")
	flag.BoolVar(&overwrite, "overwrite", false, "overwrite existing files")
	flag.BoolVar(&nsfw, "nsfw", false, "include nsfw submissions")
	allowedTypes := flag.String("type", "", "image type (png|jpe?g|gif|webp|tiff?|bmp), separate multiple values with with comma")
	minSizeOpt := flag.String("min-size", "", "minimum size in bytes, common suffixes are allowed")
	maxSizeOpt := flag.String("max-size", "", "maximum size in bytes, common suffixes are allowed")

	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: %s [options] subreddits...\n", os.Args[0])
		_, _ = fmt.Fprintln(os.Stderr, "Available options: ")
		flag.PrintDefaults()
	}

	flag.Parse()

	subreddits := flag.Args()
	if len(subreddits) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "No subreddits provided.")
		flag.Usage()
		return
	}

	var err error
	minSize, err = parseSize(*minSizeOpt)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Invalid min size: %v.\n", err)
		flag.Usage()
		return
	}
	maxSize, err = parseSize(*maxSizeOpt)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Invalid max size: %v.\n", err)
		flag.Usage()
		return
	}

	minWidth = int(*minWidthOpt)
	maxWidth = int(*maxWidthOpt)
	minHeight = int(*minHeightOpt)
	maxHeight = int(*maxHeightOpt)

	orientations := strings.Split(*orientation, ",")

	noLandscape = true
	noPortrait = true
	noSquare = true
	for _, o := range orientations {
		if o == "portrait" {
			noPortrait = false
		} else if o == "landscape" {
			noLandscape = false
		} else if o == "square" {
			noSquare = false
		} else if o == "all" {
			noPortrait = false
			noLandscape = false
			noSquare = false
		}
	}

	availableTypes := map[string]string{
		"png":  "png",
		"jpg":  "jpeg",
		"jpeg": "jpeg",
		"gif":  "gif",
		"webp": "webp",
		"tif":  "tiff",
		"tiff": "tiff",
		"bmp":  "bmp",
	}
	if *allowedTypes != "" {
		list := strings.Split(*allowedTypes, ",")
		for _, t := range list {
			tt, ok := availableTypes[t]
			if ok {
				allowTypes[tt] = struct{}{}
			}
		}
	}

	if len(allowTypes) > 0 || noLandscape || noPortrait || minWidth > 0 || minHeight > 0 || maxWidth > 0 || maxHeight > 0 {
		parseImages = true
	}

	if *search == "" {
		search = nil
	}

	singleTemplate = template.New("name")
	singleTemplate.Funcs(template.FuncMap{
		"slugify": slugify,
	})
	_, err = singleTemplate.Parse(*singleTemplateStr)
	if err != nil {
		log.Fatalf("error parsing template: %v", err)
	}

	albumTemplate = template.New("name")
	albumTemplate.Funcs(template.FuncMap{
		"slugify": slugify,
	})
	_, err = albumTemplate.Parse(*albumTemplateStr)
	if err != nil {
		log.Fatalf("error parsing template: %v", err)
	}

	httpClient = http.Client{
		Timeout: time.Second * 10,
	}
	redditClient = RedditClient{http: &httpClient}
	imgurClient = ImgurClient{http: &httpClient}

	throttler = newImmediateTicker(*throttle)
	submissions := make(chan Submission)
	go func() {
		after := make(map[string]string)
		completed := make(map[string]bool)
		for _, sub := range subreddits {
			after[sub] = ""
			completed[sub] = false
		}

		page := 1
		for {
			allCompleted := true
			for _, sub := range subreddits {
				if !completed[sub] {
					allCompleted = false
					<-throttler.C
					log.Printf("fetching page %d on r/%s", page, sub)

					var listing Listing
					var err error

					var rateLimitDuration time.Duration = 0
					for {
						if rateLimitDuration > 0 {
							time.Sleep(rateLimitDuration)
						}
						if search != nil {
							listing, err = redditClient.GetSearch(sub, SearchListingParams{
								After:  after[sub],
								Limit:  int(*pageSize),
								Search: *search,
							})
						} else {
							listing, err = redditClient.GetNew(sub, NewListingParams{
								After: after[sub],
								Limit: int(*pageSize),
							})
						}
						if err == nil {
							break
						} else if err == RateLimited {
							rateLimitDuration += *throttle
							log.Printf("rate limit reached, retrying after %s", rateLimitDuration.String())
						} else {
							log.Printf("fetching failed: %v, retrying", err)
							<-throttler.C
						}
					}

					for _, submission := range listing.Children {
						// ignore meta submissions
						if !submission.IsMeta {
							submissions <- submission
						}
					}

					if listing.After == "" {
						completed[sub] = true
						log.Printf("completed %s", sub)
					} else {
						after[sub] = listing.After
					}
				}
			}
			page += 1

			if allCompleted {
				break
			}
		}
		close(submissions)
	}()

	for submission := range submissions {
		if submission.Nsfw && !nsfw {
			log.Printf("skipping NSFW: %s (%s)", submission.Url, submission.Permalink)
		} else if submission.Score < *minScore {
			log.Printf("skipping score below %d (has %d): %s (%s)", *minScore, submission.Score, submission.Url, submission.Permalink)
		} else {
			_ = fetchSubmission(submission)
		}
	}
	log.Printf("finished")
}

func parseSize(size string) (int, error) {
	size = strings.TrimSpace(strings.ToLower(size))
	if size == "" {
		return 0, nil
	}
	var numStr string
	var suffix string
	// split into num and suffix on first non-digit rune
	for i, ch := range size {
		if !unicode.IsDigit(ch) {
			numStr = strings.TrimSpace(size[:i])
			suffix = strings.TrimSpace(size[i:])
			break
		}
	}
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, err
	}

	var factor float64
	if suffix == "" || suffix == "b" {
		factor = 1
	} else if suffix == "k" || suffix == "kb" {
		factor = 1024
	} else if suffix == "m" || suffix == "mb" {
		factor = 1024 * 1024
	} else if suffix == "g" || suffix == "gb" {
		factor = 1024 * 1024 * 1024
	} else {
		return 0, fmt.Errorf("invalid size suffix: %s", suffix)
	}
	return int(num * factor), nil
}

func fetchSubmission(submission Submission) error {
	if submission.PostHint == "image" {
		return fetchSingleImage(submission.Url, submission)
	} else if submission.Domain == "imgur.com" {
		return fetchImgur(submission)
	} else {
		return fmt.Errorf("could not fetch %s, unknown service %s", submission.Url, submission.Domain)
	}
}

func fetchSingleImage(u string, submission Submission) error {
	if skipDuplicates {
		_, exists := knownUrls[u]
		if exists {
			log.Printf("skipping %s\n", u)
			return nil
		}
		knownUrls[u] = struct{}{}
	}

	resp, err := httpClient.Get(u)
	if err != nil {
		log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
		return err
	}
	defer func() {
		_, _ = io.Copy(ioutil.Discard, resp.Body)
		err := resp.Body.Close()
		if err != nil {
			log.Printf("error closing response body: %v", err)
		}
	}()

	if resp.StatusCode == 404 || (resp.Request.URL.Host == "i.imgur.com" && strings.HasSuffix(resp.Request.URL.Path, "removed.png")) {
		log.Printf("fetching %s (%s) => not found\n", u, submission.Permalink)
		return fmt.Errorf("image not found")
	} else if resp.StatusCode >= 300 {
		log.Printf("fetching %s (%s) => HTTP status %d\n", u, submission.Permalink, resp.StatusCode)
		return fmt.Errorf("status code is not 2XX")
	}

	var data []byte
	if skipDuplicates {
		hasher := sha256.New()
		tee := io.TeeReader(resp.Body, hasher)
		data, err = ioutil.ReadAll(tee)
		if err != nil {
			log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
			return err
		}
		hash := hasher.Sum(nil)
		hashString := string(hash)
		_, exists := knownHashes[hashString]
		if exists {
			log.Printf("fetching %s (%s) => hash exists already, skipping", u, submission.Permalink)
			return nil
		}
		knownHashes[string(hash)] = struct{}{}
	} else {
		data, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
			return err
		}
	}

	if len(data) < minSize {
		log.Printf("fetching %s (%s) => smaller than %d bytes, skipping", u, submission.Permalink, minSize)
		return nil
	}
	if maxSize > 0 && len(data) > maxSize {
		log.Printf("fetching %s (%s) => greater than %d bytes, skipping", u, submission.Permalink, maxSize)
		return nil
	}

	if ok, msg := checkImage(data); !ok {
		log.Printf("fetching %s (%s) => %s, skipping", u, submission.Permalink, msg)
		return nil
	}

	parsedUrl, _ := url.Parse(u)
	ext := path.Ext(parsedUrl.Path)

	contentType := resp.Header.Get("Content-Type")

	if contentType != "" {
		exts, err := mime.ExtensionsByType(contentType)
		if err == nil && len(exts) > 0 {
			if ext == "" {
				ext = exts[0]
			} else {
				valid := false
				for _, e := range exts {
					if e == ext {
						valid = true
						break
					}
				}
				if !valid {
					ext = exts[0]
				}
			}
		}
	}

	created := time.Unix(int64(submission.CreatedUtc), 0)

	templateData := struct {
		Ext        string
		Submission Submission
		Time       time.Time
		Timestamp  string
	}{
		Ext:        ext,
		Submission: submission,
		Time:       created,
		Timestamp:  created.Format("2006-01-02-15-04-05"),
	}

	var name bytes.Buffer
	err = singleTemplate.Execute(&name, templateData)
	if err != nil {
		panic(fmt.Errorf("template error: %v", err))
	}

	p := name.String()

	if !filepath.IsAbs(p) {
		p = outputRoot + "/" + p
	}

	if !overwrite {
		if _, err := os.Stat(p); err == nil || !os.IsNotExist(err) {
			// exists or some error except "not exist"
			log.Printf("fetching %s (%s) => file exists, overwrite disabled", u, submission.Permalink)
			return nil
		}
	}

	dir := filepath.Dir(p)
	_ = os.MkdirAll(dir, 0o777)
	err = ioutil.WriteFile(p, data, 0o666)
	if err != nil {
		log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
		return err
	}
	if !quiet {
		log.Printf("fetching %s (%s) => %s", u, submission.Permalink, p)
	}
	return nil
}

func fetchImgur(submission Submission) error {
	u, err := url.Parse(submission.Url)
	if err != nil {
		log.Printf("invalid url: %s", submission.Url)
		return err
	}
	if strings.HasPrefix(u.Path, "/a/") {
		if noAlbums {
			log.Printf("skipping imgur album: %s\n", submission.Url)
			return nil
		}
		albumId := strings.TrimPrefix(u.Path, `/a/`)
		if skipDuplicates {
			_, exists := knownUrls[submission.Url]
			if exists {
				log.Printf("skipping imgur album: %s\n", submission.Url)
				return nil
			}
			knownUrls[submission.Url] = struct{}{}
		}
		album, err := imgurClient.GetAlbum(albumId)
		if err != nil {
			log.Printf("fetching imgur album: %s (%s) => %v", submission.Url, submission.Permalink, err)
			return err
		}

		for i, img := range album.Images {
			u := fmt.Sprintf(`https://i.imgur.com/%s%s`, img.Hash, img.Ext)
			if skipDuplicatesInAlbums {
				_, exists := knownUrls[u]
				if exists {
					log.Printf("skipping %s (%s)\n", u, submission.Permalink)
					continue
				}
				knownUrls[u] = struct{}{}
			}
			resp, err := httpClient.Get(u)
			if err != nil {
				log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
				continue
			}
			defer func() {
				_, _ = io.Copy(ioutil.Discard, resp.Body)
				err := resp.Body.Close()
				if err != nil {
					log.Printf("error closing response body: %v", err)
				}
			}()

			if strings.HasSuffix(resp.Request.URL.Path, "removed.png") {
				log.Printf("fetching %s (%s) => not found\n", u, submission.Permalink)
				continue
			} else if resp.StatusCode >= 300 {
				log.Printf("fetching %s (%s) => HTTP status %d", u, submission.Permalink, resp.StatusCode)
				continue
			}

			var data []byte

			if skipDuplicatesInAlbums {
				hasher := sha256.New()
				tee := io.TeeReader(resp.Body, hasher)
				data, err = ioutil.ReadAll(tee)
				if err != nil {
					log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
					continue
				}
				hash := hasher.Sum(nil)
				hashString := string(hash)
				_, exists := knownHashes[hashString]
				if exists {
					log.Printf("fetching %s (%s) => hash exists already, skipping\n", u, submission.Permalink)
					continue
				}
				knownHashes[string(hash)] = struct{}{}
			} else {
				data, err = ioutil.ReadAll(resp.Body)
				if err != nil {
					log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
					continue
				}
			}

			if len(data) < minSize {
				log.Printf("fetching %s (%s) => smaller than %d bytes, skipping", u, submission.Permalink, minSize)
				continue
			}
			if maxSize > 0 && len(data) > maxSize {
				log.Printf("fetching %s (%s) => greater than %d bytes, skipping", u, submission.Permalink, maxSize)
				continue
			}

			if ok, msg := checkImage(data); !ok {
				log.Printf("fetching %s (%s) => %s, skipping", u, submission.Permalink, msg)
				continue
			}

			created := time.Unix(int64(submission.CreatedUtc), 0)

			templateData := struct {
				Ext        string
				Submission Submission
				Image      AlbumImage
				Time       time.Time
				Timestamp  string
				Num        int
			}{
				Ext:        img.Ext,
				Submission: submission,
				Image:      img,
				Time:       created,
				Timestamp:  created.Format("2006-01-02-15-04-05"),
				Num:        i + 1,
			}

			var name bytes.Buffer
			err = albumTemplate.Execute(&name, templateData)
			if err != nil {
				panic(fmt.Errorf("template error: %v", err))
			}

			p := name.String()
			if !filepath.IsAbs(p) {
				p = outputRoot + "/" + p
			}

			if !overwrite {
				if _, err := os.Stat(p); err != nil {
					// exists or some error
					log.Printf("fetching %s (%s) => file exists, overwrite disabled", u, submission.Permalink)
					continue
				}
			}

			dir := filepath.Dir(p)
			_ = os.MkdirAll(dir, 0o777)
			err = ioutil.WriteFile(p, data, 0o666)
			if err != nil {
				log.Printf("fetching %s (%s) => %v", u, submission.Permalink, err)
				continue
			}
			if !quiet {
				log.Printf("fetching %s (%s) => %s\n", u, submission.Permalink, p)
			}
		}
		return nil
	} else {
		imgUrl := `https://i.imgur.com` + u.Path + `.png`
		return fetchSingleImage(imgUrl, submission)
	}
}

func slugify(str string) string {
	return slug.Make(str)
}

func newImmediateTicker(repeat time.Duration) *time.Ticker {
	ticker := time.NewTicker(repeat)
	oc := ticker.C
	nc := make(chan time.Time, 1)
	go func() {
		nc <- time.Now()
		for tm := range oc {
			nc <- tm
		}
	}()
	ticker.C = nc
	return ticker
}

func checkImage(data []byte) (bool, string) {
	if !parseImages {
		return true, ""
	}
	cfg, imgType, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return false, "failed to parse image"
	}
	if _, ok := allowTypes[imgType]; !ok {
		return false, fmt.Sprintf("type %s not allowed", imgType)
	}
	if noPortrait && cfg.Height > cfg.Width {
		return false, "portrait orientation"
	}
	if noLandscape && cfg.Width > cfg.Height {
		return false, "landscape orientation"
	}
	if noSquare && cfg.Width == cfg.Height {
		return false, "square orientation"
	}
	if cfg.Width < minWidth {
		return false, fmt.Sprintf("width < %d", minWidth)
	}
	if cfg.Height < minHeight {
		return false, fmt.Sprintf("height < %d", minWidth)
	}
	if maxWidth > 0 && cfg.Width > maxWidth {
		return false, fmt.Sprintf("width > %d", maxWidth)
	}
	if maxHeight > 0 && cfg.Height > maxHeight {
		return false, fmt.Sprintf("height > %d", maxHeight)
	}
	return true, ""
}
