package main

import (
	"bufio"
	"embed"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/skip2/go-qrcode"
)

var BCHAddress string = ""
var ScamThreshold float64 = 0.0001 // MINIMUM DONATION AMOUNT
var MessageMaxChar int = 250
var NameMaxChar int = 25
var username string = "admin"                // chat log /view page
var AlertWidgetRefreshInterval string = "10" //seconds
// fullstack.cash
var apiURL string = "https://api.fullstack.cash/v5/electrumx"
var transactionsMethod string = "/transactions/"
var transactionDetailsMethod string = "/tx/data/"

// this is the password for both the /view page and the OBS /alert page
// example OBS url: https://example.com/alert?auth=adminadmin
var password string = "adminadmin"
var checked string = ""

// Email settings
var enableEmail bool = false
var smtpHost string = "smtp.purelymail.com"
var smtpPort string = "587"
var smtpUser string = "example@purelymail.com"
var smtpPass string = "[y7EQ(xgTW_~{CUpPhO6(#"
var sendTo = []string{"example@purelymail.com"} // Comma separated recipient list

var indexTemplate *template.Template
var payTemplate *template.Template
var checkTemplate *template.Template
var alertTemplate *template.Template
var viewTemplate *template.Template
var topWidgetTemplate *template.Template

type configJson struct {
	BCHAddress       string   `json:"BCHAddress"`
	MinimumDonation  float64  `json:"MinimumDonation"`
	MaxMessageChars  int      `json:"MaxMessageChars"`
	MaxNameChars     int      `json:"MaxNameChars"`
	WebViewUsername  string   `json:"WebViewUsername"`
	WebViewPassword  string   `json:"WebViewPassword"`
	OBSWidgetRefresh string   `json:"OBSWidgetRefresh"`
	Checked          bool     `json:"ShowAmountCheckedByDefault"`
	EnableEmail      bool     `json:"EnableEmail"`
	SMTPServer       string   `json:"SMTPServer"`
	SMTPPort         string   `json:"SMTPPort"`
	SMTPUser         string   `json:"SMTPUser"`
	SMTPPass         string   `json:"SMTPPass"`
	SendToEmail      []string `json:"SendToEmail"`
}

type checkPage struct {
	Addy     string
	PayID    string
	Received float64
	Meta     string
	Name     string
	Msg      string
	Receipt  string
}

type superChat struct {
	Name     string
	Message  string
	Amount   string
	Address  string
	QRB64    string
	PayID    string
	CheckURL string
}

type csvLog struct {
	ID            string
	Name          string
	Message       string
	Amount        string
	DisplayToggle string
	Refresh       string
}

type indexDisplay struct {
	MaxChar int
	MinAmnt float64
	Checked string
}

type viewPageData struct {
	ID      []string
	Name    []string
	Message []string
	Amount  []string
	Display []string
}

type transactionsResponse struct {
	Success      bool `json:"success"`
	Transactions []struct {
		Height  int    `json:"height"`
		Tx_Hash string `json:"tx_hash"`
	}
}

type transactionsDetailsResponse struct {
	Success      bool `json:"success"`
	Transactions []struct {
		Details struct {
			Vout []struct {
				Value float64 `json:"value"`
			}
		}
		TxId string `json:"txid"`
	}
}

//go:embed web/*
var resources embed.FS

//go:embed style
var styleFiles embed.FS

//go:embed config.json
var configBytes []byte

func main() {

	var conf configJson
	err := json.Unmarshal(configBytes, &conf)
	if err != nil {
		panic(err) // Fatal error, stop program
	}

	BCHAddress = conf.BCHAddress
	ScamThreshold = conf.MinimumDonation
	MessageMaxChar = conf.MaxMessageChars
	NameMaxChar = conf.MaxNameChars
	username = conf.WebViewUsername
	password = conf.WebViewPassword
	AlertWidgetRefreshInterval = conf.OBSWidgetRefresh
	enableEmail = conf.EnableEmail
	smtpHost = conf.SMTPServer
	smtpPort = conf.SMTPPort
	smtpUser = conf.SMTPUser
	smtpPass = conf.SMTPPass
	sendTo = conf.SendToEmail
	if conf.Checked == true {
		checked = " checked"
	}

	fmt.Println(BCHAddress)

	fmt.Println(fmt.Sprintf("email notifications enabled?: %t", enableEmail))
	fmt.Println(fmt.Sprintf("OBS Alert path: /alert?auth=%s", password))

	var styleFS = http.FS(styleFiles)
	fs := http.FileServer(styleFS)
	http.Handle("/style/", fs)

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/pay", paymentHandler)
	http.HandleFunc("/check", checkHandler)
	http.HandleFunc("/alert", alertHandler)
	http.HandleFunc("/view", viewHandler)
	http.HandleFunc("/top", topwidgetHandler)

	// Create files and directory if they don't exist
	path := "log"
	_ = os.Mkdir(path, os.ModePerm)

	_, err = os.OpenFile("log/paid.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}

	_, err = os.OpenFile("log/alertqueue.csv", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}

	_, err = os.OpenFile("log/superchats.csv", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}

	indexTemplate, _ = template.ParseFS(resources, "web/index.html")
	payTemplate, _ = template.ParseFS(resources, "web/pay.html")
	checkTemplate, _ = template.ParseFS(resources, "web/check.html")
	alertTemplate, _ = template.ParseFS(resources, "web/alert.html")
	viewTemplate, _ = template.ParseFS(resources, "web/view.html")
	topWidgetTemplate, _ = template.ParseFS(resources, "web/top.html")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8900"
	}
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Println("rompio aca")
		panic(err)
	}

}
func mail(name string, amount string, message string) {
	body := []byte(fmt.Sprintf("From: %s\n"+
		"Subject: %s sent %s BCH\nDate: %s\n\n"+
		"%s", smtpUser, name, amount, fmt.Sprint(time.Now().Format(time.RFC1123Z)), message))

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	err := smtp.SendMail(smtpHost+":"+smtpPort, auth, smtpUser, sendTo, body)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("email sent")
}

func condenseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
func truncateStrings(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}
func reverse(ss []string) {
	last := len(ss) - 1
	for i := 0; i < len(ss)/2; i++ {
		ss[i], ss[last-i] = ss[last-i], ss[i]
	}
}

func viewHandler(w http.ResponseWriter, r *http.Request) {
	var a viewPageData
	var displayTemp string

	u, p, ok := r.BasicAuth()
	if !ok {
		w.Header().Add("WWW-Authenticate", `Basic realm="Give username and password"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if (u == username) && (p == password) {
		csvFile, err := os.Open("log/superchats.csv")
		if err != nil {
			fmt.Println(err)
		}

		defer func(csvFile *os.File) {
			err := csvFile.Close()
			if err != nil {
				fmt.Println(err)
			}
		}(csvFile)

		csvLines, err := csv.NewReader(csvFile).ReadAll()
		if err != nil {
			fmt.Println(err)
		}

		for _, line := range csvLines {
			a.ID = append(a.ID, line[0])
			a.Name = append(a.Name, line[1])
			a.Message = append(a.Message, line[2])
			a.Amount = append(a.Amount, line[3])
			displayTemp = fmt.Sprintf("<h3><b>%s</b> sent <b>%s</b> BCH:</h3><p>%s</p>", html.EscapeString(line[1]), html.EscapeString(line[3]), line[2])
			a.Display = append(a.Display, displayTemp)
		}

	} else {
		w.WriteHeader(http.StatusUnauthorized)
		return // return http 401 unauthorized error
	}
	reverse(a.Display)
	err := viewTemplate.Execute(w, a)
	if err != nil {
		fmt.Println(err)
	}
}

func checkHandler(w http.ResponseWriter, r *http.Request) {

	var c checkPage
	c.Meta = `<meta http-equiv="Refresh" content="5">`
	c.Addy = BCHAddress
	c.Received, _ = strconv.ParseFloat(r.FormValue("amount"), 64)
	c.Name = truncateStrings(r.FormValue("name"), NameMaxChar)
	c.Msg = truncateStrings(r.FormValue("msg"), MessageMaxChar)
	c.Receipt = "Waiting for payment..."

	var txsWallet []string
	getTXs(&txsWallet)
	var txsPaidLog []string
	getPaidLogTxs(&txsPaidLog)
	for _, txToRemove := range txsPaidLog {
		txsWallet = remove(txsWallet, txToRemove)
	}

	txsBatchSize := 20

	for i := 0; i < len(txsWallet); i += txsBatchSize {
		j := i + txsBatchSize
		if j > len(txsWallet) {
			j = len(txsWallet)
		}
		txsBatch := txsWallet[i:j]
		txsDetailsResp := &transactionsDetailsResponse{}
		getTxsDetailsResponse(txsDetailsResp, txsBatch)

		for _, tx := range txsDetailsResp.Transactions {
			for _, vout := range tx.Details.Vout {
				if vout.Value == c.Received {
					appendTxToLog(tx.TxId)

					c.Meta = ""
					setCheckReceipt(&c.Receipt, c.Received)

					if c.Msg == "" {
						c.Msg = "⠀"
					}
					c.PayID = tx.TxId
					if c.Received >= ScamThreshold {
						appendTxToCSVs(c.PayID, c.Name, c.Msg, c.Received, r.FormValue("show"))
						checkMailAndSend(c.Name, c.Received, c.Msg, r.FormValue("show"))
					}
				}
			}
		}

	}

	err := checkTemplate.Execute(w, c)
	if err != nil {
		fmt.Println(err)
	}
}

func checkMailAndSend(cName string, cReceived float64, cMsg string, show string) {
	if enableEmail {
		if show != "true" {
			mail(cName, fmt.Sprint(cReceived)+" (hidden)", cMsg)
		} else {
			mail(cName, fmt.Sprint(cReceived), cMsg)
		}
	}
}

func appendTxToCSVs(cPayID string, cName string, cMsg string, cReceived float64, show string) {
	f, err := os.OpenFile("log/superchats.csv",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			fmt.Println(err)
		}
	}(f)
	csvAppend := fmt.Sprintf(`"%s","%s","%s","%s"`, cPayID, html.EscapeString(cName), html.EscapeString(cMsg), fmt.Sprint(cReceived))
	if show != "true" {
		csvAppend = fmt.Sprintf(`"%s","%s","%s","%s (hidden)"`, cPayID, html.EscapeString(cName), html.EscapeString(cMsg), fmt.Sprint(cReceived))
	}
	a, err := os.OpenFile("log/alertqueue.csv",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer func(a *os.File) {
		err := a.Close()
		if err != nil {
			fmt.Println(err)
		}
	}(a)
	fmt.Println(csvAppend)

	if _, err := f.WriteString(csvAppend + "\n"); err != nil {
		log.Println(err)
	}

	if show != "true" {
		csvAppend = fmt.Sprintf(`"%s","%s","%s","???"`, cPayID, html.EscapeString(cName), html.EscapeString(cMsg))
	}

	if _, err := a.WriteString(csvAppend + "\n"); err != nil {
		log.Println(err)
	}
}

func setCheckReceipt(receiptPtr *string, received float64) {
	if received < ScamThreshold {
		*receiptPtr = "<b style='color:red'>Scammed! " + fmt.Sprint(received) + " is below minimum</b>"
	} else {
		*receiptPtr = "<b>" + fmt.Sprint(received) + " BCH Received! Superchat sent</b>"
	}
}

func getTxsDetailsResponse(txsDetailsResp *transactionsDetailsResponse, txHashes []string) {
	txs := strings.Join(txHashes, `","`)
	payloadTxt := `{ "txids" : ["` + txs + `"], "verbose": false }`
	payload := strings.NewReader(payloadTxt)
	reqTxDet, _ := http.NewRequest("POST", apiURL+transactionDetailsMethod, payload)
	reqTxDet.Header.Set("Content-Type", "application/json")
	respTxDet, _ := http.DefaultClient.Do(reqTxDet)
	if err := json.NewDecoder(respTxDet.Body).Decode(txsDetailsResp); err != nil {
		fmt.Println(err.Error())
	}
}

func getTXs(txHashes *[]string) {
	res, err := http.Get(apiURL + transactionsMethod + BCHAddress)
	if err == nil {
		txResp := &transactionsResponse{}
		if err := json.NewDecoder(res.Body).Decode(txResp); err != nil {
			fmt.Println(err.Error())
		}

		for _, tx := range txResp.Transactions {
			*txHashes = append(*txHashes, tx.Tx_Hash)
		}
	}
}

func getPaidLogTxs(txsPaidLog *[]string) {
	file, err := os.Open("log/paid.log")
	if err != nil {
		log.Fatalf("failed to open ")
	}
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		*txsPaidLog = append(*txsPaidLog, scanner.Text())
	}
	err = file.Close()
	if err != nil {
		fmt.Println(err)
	}
}

func appendTxToLog(txId string) {
	f, err := os.OpenFile("log/paid.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			fmt.Println(err)
		}
	}(f)
	if _, err := f.WriteString(txId + "\n"); err != nil {
		log.Println(err)
	}
}

func remove(stringSlice []string, stringToRemove string) []string {
	for i, v := range stringSlice {
		if v == stringToRemove {
			return append(stringSlice[:i], stringSlice[i+1:]...)
		}
	}
	return stringSlice
}

func indexHandler(w http.ResponseWriter, _ *http.Request) {
	var i indexDisplay
	i.MaxChar = MessageMaxChar
	i.MinAmnt = ScamThreshold
	i.Checked = checked
	err := indexTemplate.Execute(w, i)
	if err != nil {
		fmt.Println(err)
	}
}
func topwidgetHandler(w http.ResponseWriter, r *http.Request) {
	u, p, ok := r.BasicAuth()
	if !ok {
		w.Header().Add("WWW-Authenticate", `Basic realm="Give username and password"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if (u == username) && (p == password) {
		csvFile, err := os.Open("log/superchats.csv")
		if err != nil {
			fmt.Println(err)
		}
		defer func(csvFile *os.File) {
			err := csvFile.Close()
			if err != nil {
				fmt.Println(err)
			}
		}(csvFile)

		// TODO: Add an OBS widget displaying top n donors. Don't include amounts set as hidden by donor

		//csvLines, err := csv.NewReader(csvFile).ReadAll()
		//if err != nil {
		//	fmt.Println(err)
		//}

	} else {
		w.WriteHeader(http.StatusUnauthorized)
		return // return http 401 unauthorized error
	}
	err := topWidgetTemplate.Execute(w, nil)
	if err != nil {
		fmt.Println(err)
	}
}

func alertHandler(w http.ResponseWriter, r *http.Request) {
	var v csvLog
	v.Refresh = AlertWidgetRefreshInterval
	if r.FormValue("auth") == password {

		csvFile, err := os.Open("log/alertqueue.csv")
		if err != nil {
			fmt.Println(err)
		}

		csvLines, err := csv.NewReader(csvFile).ReadAll()
		if err != nil {
			fmt.Println(err)
		}
		defer func(csvFile *os.File) {
			err := csvFile.Close()
			if err != nil {
				fmt.Println(err)
			}
		}(csvFile)

		// Remove top line of CSV file after displaying it
		if csvLines != nil {
			popFile, _ := os.OpenFile("log/alertqueue.csv", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
			popFirst := csvLines[1:]
			w := csv.NewWriter(popFile)
			err := w.WriteAll(popFirst)
			if err != nil {
				fmt.Println(err)
			}
			defer func(popFile *os.File) {
				err := popFile.Close()
				if err != nil {
					fmt.Println(err)
				}
			}(popFile)
			v.ID = csvLines[0][0]
			v.Name = csvLines[0][1]
			v.Message = csvLines[0][2]
			v.Amount = csvLines[0][3]
			v.DisplayToggle = ""
		} else {
			v.DisplayToggle = "display: none;"
		}
	} else {
		w.WriteHeader(http.StatusUnauthorized)
		return // return http 401 unauthorized error
	}
	err := alertTemplate.Execute(w, v)
	if err != nil {
		fmt.Println(err)
	}
}

func paymentHandler(w http.ResponseWriter, r *http.Request) {
	if BCHAddress != "" {
		var s superChat
		s.Amount = html.EscapeString(r.FormValue("amount"))
		if r.FormValue("amount") == "" {
			s.Amount = fmt.Sprint(ScamThreshold)
		}
		if r.FormValue("name") == "" {
			s.Name = "Anonymous"
		} else {
			s.Name = html.EscapeString(truncateStrings(condenseSpaces(r.FormValue("name")), NameMaxChar))
		}
		s.Message = html.EscapeString(truncateStrings(condenseSpaces(r.FormValue("message")), MessageMaxChar))
		s.Address = BCHAddress

		params := url.Values{}
		params.Add("amount", s.Amount)
		params.Add("name", s.Name)
		params.Add("msg", r.FormValue("message"))
		params.Add("show", html.EscapeString(r.FormValue("showAmount")))
		s.CheckURL = params.Encode()

		tmp, _ := qrcode.Encode(fmt.Sprintf("%s?amount=%s", BCHAddress, s.Amount), qrcode.Low, 320)
		s.QRB64 = base64.StdEncoding.EncodeToString(tmp)

		err := payTemplate.Execute(w, s)
		if err != nil {
			fmt.Println(err)
		}
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		return // return http 401 unauthorized error
	}
}
