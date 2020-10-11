# reddit-image-downloader

Downloads all (available) images from given subreddits. 

No authentication/api-key is required as the publicly available endpoints `/r/<subreddit>/new.json` and `/r/<subreddit>/search.json` are used.

By default, single images are stored at `<subreddit name>/<timestamp>-<reddit id>-<slugified name>.<ext>` and imgur albums are stored at `<subreddit name>/<timestamp>-<reddit id>-<slugified name>/<number>-<imgur hash>.<ext>`.
These paths can be freely configured via Go text templates. 

The output directory can be supplied with the `-out` option and defaults to the working directory. Absolute paths in the path templates (e.g. `-single-template '/home/username/download/{{.Submission.Id}}{{.Ext}}'`) ignore the output directory.

Duplicates are skipped by default (before download based on URL, additionally after download based on sha256 hash) except in imgur albums.

## Installation
[Download the binary](https://github.com/sammax/reddit-image-downloader/releases) or build it yourself (requires Go 1.13 or newer):
```shell script
$ git clone --depth=1 https://github.com/sammax/reddit-image-downloader
$ cd reddit-image-downloader
$ go build .
```

## Usage
```
Available options:
  -album-template string
        template for image paths in albums, use go template syntax (default "{{.Submission.Subreddit}}/{{.Timestamp}}-{{.Submission.Id}}-{{.Submission.Title | slugify}}/{{.Num}}-{{.Image.Hash}}{{.Ext}}")
  -max-height uint
        maximum height (0 = off)
  -max-width uint
        maximum width (0 = off)
  -min-height uint
        minimum height
  -min-score int
        ignore submissions below this score
  -min-width uint
        minimum width
  -no-albums
        don't download albums
  -nsfw
        include nsfw submissions
  -orientation string
        image orientation (landscape/portrait/square/all), separate multiple values with comma (default "all")
  -out string
        root output directory (default ".")
  -overwrite
        overwrite existing files
  -page-size uint
        reddit api listing page size (default 25)
  -quiet
        don't print every submission (errors and skips are still printed)
  -search string
        search string
  -single-template string
        template for image paths, use go template syntax (default "{{.Submission.Subreddit}}/{{.Timestamp}}-{{.Submission.Id}}-{{.Submission.Title | slugify}}{{.Ext}}")
  -skip-duplicates
        skip duplicate single images (default true)
  -skip-duplicates-in-albums
        skip duplicate images within imgur albums
  -throttle duration
        wait at least this long between requests to the reddit api (default 2s)
```

## Examples
All images from `cute` and `aww`:
```shell script
$ reddit-image-downloader cute aww
```
All Images from `pics` that have a score of at least 100:
```shell script
$ reddit-image-downloader -min-score 100 pics 
```
All images from `animewallpaper` that have the `Desktop` flair:
```shell script
$ reddit-image-downloader -search 'flair:Desktop' animewallpaper
```
Store single images at `<reddit id>.<ext>` and albums at `<reddit id>/<num>.<ext>`:
```shell script
$ reddit-image-downloader -single-template '{{.Submission.Id}}{{.Ext}}' -album-template '{{.Submission.Id}}/{{.Num}}{{.Ext}}' wallpapers
```

## Template data
The following data is available for the path templates:
```shell script
.Submission: reddit data
  .Title
  .Name: internal name (is always 't3_' + Id)
  .Id: internal id
  .Domain: domain of the image url
  .Author: reddit author
  .CreatedUtc: creation timestamp as integer
  .Url: image url
  .Permalink: reddit link (without 'https://reddit.com')
  .Subreddit: subreddit name
  .Nsfw
  .Score
.Image: imgur album data (only available in album template)
  .Hash: imgur id
  .Title: imgur title
.Num: position of image in album (only available in album template)
.Ext: extension with leading '.', empty if no extension
.Time: reddit creation timestamp as time.Time
.Timestamp: reddit creation timestamp as string in format YYYY-MM-DD-hh-mm-ss
```
Additionally to the default pipeline functions, the function `slugify` is available, which creates a filepath-friendly version of a string. Example usage:
```shell script
$ reddit-template-downloader -single-template '{{.Submission.Title | slugify}}{{.Ext}}' pics
```
