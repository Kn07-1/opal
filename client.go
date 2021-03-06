/*
Package opal provides programmatic access to Opal card information.
*/
package opal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Client is an interface to the online Opal system.
type Client struct {
	hc *http.Client

	as AuthStore
	a  *Auth
}

// Auth holds the authentication information for accessing Opal.
type Auth struct {
	Username, Password string
	Cookies            []*http.Cookie
}

var cookieBaseURL = &url.URL{
	Scheme: "https",
	Host:   "www.opal.com.au",
}

// NewClient constructs a new Client.
func NewClient(as AuthStore) (*Client, error) {
	a, err := as.Load()
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	jar.SetCookies(cookieBaseURL, a.Cookies)

	c := &Client{
		hc: &http.Client{
			Jar: jar,
		},
		as: as,
		a:  a,
	}
	c.hc.CheckRedirect = c.checkRedirect
	return c, nil
}

// WriteConfig writes the configuration to the client's AuthStore.
func (c *Client) WriteConfig() error {
	c.a.Cookies = c.hc.Jar.Cookies(cookieBaseURL)
	return c.as.Save(c.a)
}

// Overview fetches the account overview.
func (c *Client) Overview() (*Overview, error) {
	body, err := c.get("https://www.opal.com.au/registered/index")
	if err != nil {
		return nil, err
	}
	return parseOverview(body)
}

// An ActivityRequest configures the operation of Activity.
type ActivityRequest struct {
	CardIndex int
	// Offset is how many pages into the past to fetch.
	// Zero is the most recent activity.
	Offset int
}

// Activity fetches a subset of the activity data for a card.
func (c *Client) Activity(req ActivityRequest) (*Activity, error) {
	u := fmt.Sprintf("https://www.opal.com.au/registered/opal-card-transactions/?cardIndex=%d", req.CardIndex)
	if req.Offset > 0 {
		u += fmt.Sprintf("&pageIndex=%d", req.Offset)
	}
	body, err := c.get(u)
	if err != nil {
		return nil, err
	}
	return parseActivity(body)
}

var errRedirect = errors.New("internal error: login redirect detected")

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if strings.HasPrefix(req.URL.Path, "/login/") {
		return errRedirect
	}
	return fmt.Errorf("hit redirect for %v", req.URL) // shouldn't happen
}

func (c *Client) get(u string) (body []byte, err error) {
	var resp *http.Response
	for try := 1; try <= 2; try++ {
		resp, err = c.hc.Get(u)
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		if err == errRedirect {
			if err = c.login(); err == nil {
				continue // next try
			}
		}
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	body, err = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err == nil && resp.StatusCode != 200 {
		err = fmt.Errorf("HTTP response %s", resp.Status)
	}
	return body, err
}

func (c *Client) login() error {
	body, err := c.get("https://www.opal.com.au/login/index")
	if err != nil {
		return fmt.Errorf("GETting login form: %v", err)
	}
	token, err := parseLogin(body)
	if err != nil {
		return err
	}
	form := url.Values{
		"h_username": []string{c.a.Username},
		"h_password": []string{c.a.Password},
		"CSRFToken":  []string{token},
	}
	resp, err := c.hc.PostForm("https://www.opal.com.au/login/registeredUserUsernameAndPasswordLogin", form)
	if err != nil {
		return fmt.Errorf("POSTing login form: %v", err)
	}
	_, err = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("reading login form response: %v", err)
	}
	// A successful response sets a cookie in c.hc.
	if resp.StatusCode != 200 {
		return fmt.Errorf("login form response was %s", resp.Status)
	}
	return nil
}

// An AuthStore is an interface for loading and saving authentication information.
// See FileAuthStore for a file-based implementation.
type AuthStore interface {
	Load() (*Auth, error)
	Save(*Auth) error
}

// DefaultAuthFile is a default place to store authentication information.
// Pass this to FileAuthStore if an alternate path isn't required.
var DefaultAuthFile = filepath.Join(os.Getenv("HOME"), ".opal")

// FileAuthStore returns an AuthStore that stores authentication information in a named file.
func FileAuthStore(filename string) AuthStore {
	return fileAuthStore{filename}
}

type fileAuthStore struct {
	filename string
}

func (f fileAuthStore) Load() (*Auth, error) {
	// Security check.
	fi, err := os.Stat(f.filename)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&0077 != 0 {
		return nil, fmt.Errorf("security check failed on %s: mode is %04o; it should not be accessible by group/other", f.filename, fi.Mode())
	}

	raw, err := ioutil.ReadFile(f.filename)
	if err != nil {
		return nil, err
	}
	a := new(Auth)
	if err := json.Unmarshal(raw, a); err != nil {
		return nil, fmt.Errorf("bad auth file %s: %v", f.filename, err)
	}
	return a, nil
}

func (f fileAuthStore) Save(a *Auth) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(f.filename, raw, 0600)
}
