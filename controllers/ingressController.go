package controllers

import (
	"concierge/config"
	"concierge/database"
	"concierge/models"
	"concierge/pkg"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type info struct {
	Userinfo  models.Users
	Leaseinfo models.Leases
}

//ShowAllowedIngress ...
func ShowAllowedIngress(c *gin.Context) {
	clientset := config.KubeClient.ClientSet
	User, _ := c.Get("User")

	myclientset := pkg.MyClientSet{clientset}
	ns := ""
	ns = c.Query("ns")
	log.Infof("Listing ingress in namespace %s for user %s\n", ns, User.(*models.Users).Email)
	data, err := myclientset.GetIngresses(ns)
	if err != nil {
		log.Error("Error", err)
		return
	}
	c.HTML(http.StatusOK, "showingresslist.gohtml", gin.H{
		"data": data,
		"user": User,
	})
}

//WhiteListIP ...
func WhiteListIP(c *gin.Context) {
	clientset := config.KubeClient.ClientSet

	User, _ := c.Get("User")
	myclientset := pkg.MyClientSet{clientset}
	ns := c.Param("ns")
	name := c.Param("name")
	expiry, _ := strconv.Atoi(c.PostForm("expiry"))
	data, err := myclientset.GetIngress(ns, name)
	if err != nil {
		log.Error("Error", err)
		return
	}
	ips := c.Request.Header["X-Forwarded-For"][0]
	ip := strings.Split(ips, ",")[0]
	ip = ip + "/32"
	updateStatus, err := myclientset.WhiteListIP(ns, name, ip)
	var leases []models.Leases
	if updateStatus {
		msgInfo := "Whitelisted IP " + ip + " to ingress " + name + " in namespace " + ns + " for user " + User.(*models.Users).Email
		slackNotification(msgInfo, User.(*models.Users).Email)
		log.Info(msgInfo)
		db, err := database.Conn()
		if err != nil {
			log.Error("Error", err)
		}
		defer db.Close()

		lease := models.Leases{
			UserID:    User.(*models.Users).ID,
			LeaseIP:   ip,
			LeaseType: "Ingress",
			GroupID:   ns + ":" + name,
			Expiry:    uint(expiry),
		}

		db.Create(&lease)
		leases = GetActiveLeases(ns, name)

		c.HTML(http.StatusOK, "manageingress.gohtml", gin.H{
			"data": data,
			"user": User,
			"message": map[string]string{
				"class":   "Success",
				"message": "Lease is successfully taken",
			},
			"activeLeases": leases,
		})
		return
	}
	if err != nil {
		log.Error("Error", err)
		return
	}
	leases = GetActiveLeases(ns, name)
	c.HTML(http.StatusOK, "manageingress.gohtml", gin.H{
		"data": data,
		"user": User,
		"message": map[string]string{
			"class":   "Danger",
			"message": "Your IP is already present",
		},
		"activeLeases": leases,
	})
}

//DeleteIPFromIngress ...
func DeleteIPFromIngress(c *gin.Context) {
	clientset := config.KubeClient.ClientSet

	User, _ := c.Get("User")

	myclientset := pkg.MyClientSet{clientset}
	ns := c.Param("ns")
	name := c.Param("name")
	leaseID, err := strconv.Atoi(c.Param("id"))
	ID := uint(leaseID)
	data, err := myclientset.GetIngress(ns, name)
	if err != nil {
		log.Error("Error", err)
		return
	}
	ips := c.Request.Header["X-Forwarded-For"][0]
	ip := strings.Split(ips, ",")[0]
	ip = ip + "/32"
	updateStatus, err := DeleteLeases(ns, name, ip, ID)
	leases := GetActiveLeases(ns, name)
	if updateStatus {
		msgInfo := "Removed IP " + ip + " from ingress " + name + " in namespace " + ns + " for user " + User.(*models.Users).Email
		slackNotification(msgInfo, User.(*models.Users).Email)
		log.Info(msgInfo)
		c.HTML(http.StatusOK, "manageingress.gohtml", gin.H{
			"data":         data,
			"user":         User,
			"activeLeases": leases,
			"message": map[string]string{
				"class":   "Success",
				"message": "Lease is successfully deleted",
			},
		})
		return
	}
	if err != nil {
		log.Error("Error", err)
		return
	}
}

//IngressDetails ...
func IngressDetails(c *gin.Context) {
	clientset := config.KubeClient.ClientSet

	User, _ := c.Get("User")
	myclientset := pkg.MyClientSet{clientset}
	ns := c.Param("ns")
	name := c.Param("name")
	leases := GetActiveLeases(ns, name)
	data, err := myclientset.GetIngress(ns, name)
	if err != nil {
		log.Error("Error", err)
		c.HTML(http.StatusNotFound, "manageingress.gohtml", gin.H{
			"message": map[string]string{
				"class":   "Danger",
				"message": err.Error(),
			},
			"user":         User,
			"activeLeases": leases,
		})
		return
	}
	c.HTML(http.StatusOK, "manageingress.gohtml", gin.H{
		"data":         data,
		"user":         User,
		"activeLeases": leases,
	})
}

//GetActiveLeases ...
func GetActiveLeases(ns string, name string) []models.Leases {
	db, err := database.Conn()
	if err != nil {
		log.Error("Error", err)
	}
	defer db.Close()
	leases := []models.Leases{}
	if ns == "" && name == "" {
		db.Preload("User").Where(models.Leases{
			LeaseType: "Ingress",
		}).Find(&leases)
	} else {
		db.Preload("User").Where(models.Leases{
			LeaseType: "Ingress",
			GroupID:   ns + ":" + name,
		}).Find(&leases)
	}
	myleases := []models.Leases{}
	for i, lease := range leases {
		t := uint(lease.CreatedAt.Unix()) + lease.Expiry
		if t < uint(time.Now().Unix()) {
			leases[i].Expiry = uint(0)
			DeleteLeases(ns, name, lease.LeaseIP, lease.ID)
			log.Infof("Removed expired IP %s from ingress %s in namespace %s for User %s\n", lease.LeaseIP, name, ns, lease.User.Email)
		} else {
			leases[i].Expiry = t - uint(time.Now().Unix())
			myleases = append(myleases, leases[i])
		}
	}
	return myleases
}

//DeleteLeases ...
func DeleteLeases(ns string, name string, ip string, ID uint) (bool, error) {
	clientset := config.KubeClient.ClientSet

	myclientset := pkg.MyClientSet{clientset}
	db, err := database.Conn()
	if err != nil {
		log.Error("Error", err)
	}
	defer db.Close()

	db.Delete(models.Leases{
		ID: ID,
	})
	updateStatus, err := myclientset.RemoveIngressIP(ns, name, ip)
	return updateStatus, err
}

//ClearExpiredLeases ...
func ClearExpiredLeases(c *gin.Context) {
	GetActiveLeases("", "")
	c.String(200, "Done")
}

func slackNotification(msg string, user string) {
	payload := pkg.Payload{
		Title:      "Concierge",
		Pretext:    msg,
		Text:       msg,
		Color:      "#36a64f",
		AuthorName: user,
		TitleLink:  "",
		Footer:     "Concierge",
		Timestamp:  strconv.FormatInt(time.Now().Unix(), 10),
	}
	payloads := pkg.Payloads{
		Attachments: map[string][]pkg.Payload{
			"attachments": []pkg.Payload{
				payload,
			},
		},
	}
	payloads.SlackNotification(os.Getenv("SLACK_WEBHOOK_URL"))
}
