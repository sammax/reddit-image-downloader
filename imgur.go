package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
)

type ImgurClient struct {
	http *http.Client
}

func (i ImgurClient) GetAlbum(id string) (Album, error) {
	u := fmt.Sprintf(`https://imgur.com/ajaxalbums/getimages/%s`, id)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return Album{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "reddit image downloader")

	res, err := i.http.Do(req)
	if err != nil {
		return Album{}, err
	}
	defer func() {
		io.Copy(ioutil.Discard, res.Body)
		res.Body.Close()
	}()
	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		return Album{}, err
	}
	var album Album
	err = json.Unmarshal(body, &album)
	return album, err
}

type Album struct {
	AlbumData `json:"data"`
	Success   bool
	Status    int
}

type AlbumData struct {
	Count  int
	Images []AlbumImage
}

type AlbumImage struct {
	Hash     string
	Title    string
	Ext      string
	Datetime string
}