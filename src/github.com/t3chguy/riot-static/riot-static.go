// Copyright 2017 Michael Telatynski <7t3chguy@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/t3chguy/riot-static/mxclient"
	"github.com/t3chguy/riot-static/utils"
	"net/http"
	"os"
	"strconv"
	"time"
)

// TODO Cache memberList+serverList until it changes

const PublicRoomsPageSize = 20
const RoomTimelineSize = 20
const RoomMembersPageSize = 20

func main() {
	client := mxclient.NewClient()

	templates := InitTemplates(client)

	router := gin.Default()
	router.SetHTMLTemplate(templates)
	router.Static("/img", "./assets/img")

	router.GET("/", func(c *gin.Context) {
		page, skip, end := utils.CalcPaginationPage(c.DefaultQuery("page", "1"), PublicRoomsPageSize)
		c.HTML(http.StatusOK, "rooms.html", gin.H{
			"Rooms": client.GetRoomList(skip, end),
			"Page":  page,
		})
	})

	roomRouter := router.Group("/room/")
	{
		// Load room into request object so that we can do any clean up etc here
		roomRouter.Use(func(c *gin.Context) {
			roomID := c.Param("roomID")

			if room := client.GetRoom(roomID); room != nil {
				if room.LazyInitialSync() {
					c.Set("Room", room)
					c.Next()
				} else {
					c.HTML(http.StatusInternalServerError, "room_error.html", gin.H{
						"Error": "Failed to load room.",
						"Room":  room,
					})
					c.Abort()
				}
			} else {
				c.String(http.StatusNotFound, "Room Not Found")
				c.Abort()
			}
		})

		roomRouter.GET("/:roomID/", func(c *gin.Context) {
			c.Redirect(http.StatusTemporaryRedirect, "chat")
		})

		roomRouter.GET("/:roomID/chat", func(c *gin.Context) {
			room := c.MustGet("Room").(*mxclient.Room)

			pageSize := RoomTimelineSize
			eventID := c.DefaultQuery("anchor", "")

			var offset int
			if offsetStr, exists := c.GetQuery("offset"); exists {
				num, err := strconv.Atoi(offsetStr)
				if err == nil {
					offset = num
				}
			}

			events, eventsErr := room.GetEventPage(eventID, offset, pageSize)

			if eventsErr != mxclient.RoomEventsFine {
				var errString string
				switch eventsErr {
				case mxclient.RoomEventsCouldNotFindEvent:
					errString = "Given up while looking for given event."
				case mxclient.RoomEventsUnknownError:
					errString = "Unknown error encountered."
				}
				c.HTML(http.StatusInternalServerError, "room_error.html", gin.H{
					"Error": errString,
					"Room":  room,
				})
				return // Bail early
			}

			if eventID == "" && len(events) > 0 {
				eventID = events[0].ID
			}

			events = mxclient.ReverseEventsCopy(events)

			var reachedRoomCreate bool
			if len(events) > 0 {
				reachedRoomCreate = events[0].Type == "m.room.create" && *events[0].StateKey == ""
			}

			c.HTML(http.StatusOK, "room.html", gin.H{
				"Room":     room,
				"Events":   events,
				"PageSize": pageSize,

				"ReachedRoomCreate": reachedRoomCreate,
				"CurrentOffset":     offset,
				"Anchor":            eventID,
			})
		})

		roomRouter.GET("/:roomID/servers", func(c *gin.Context) {
			c.HTML(http.StatusOK, "room_servers.html", gin.H{
				"Room": c.MustGet("Room").(*mxclient.Room),
			})
		})

		roomRouter.GET("/:roomID/members", func(c *gin.Context) {
			page, skip, end := utils.CalcPaginationPage(c.DefaultQuery("page", "1"), RoomMembersPageSize)
			room := c.MustGet("Room").(*mxclient.Room)

			c.HTML(http.StatusOK, "room_members.html", gin.H{
				"Room":       room,
				"MemberInfo": room.GetMembers()[skip:end],
				"Page":       page,
			})
		})

		roomRouter.GET("/:roomID/members/:mxid", func(c *gin.Context) {
			room := c.MustGet("Room").(*mxclient.Room)
			mxid := c.Param("mxid")

			if memberInfo, exists := room.GetMember(mxid); exists {
				c.HTML(http.StatusOK, "member_info.html", gin.H{
					"MemberInfo": memberInfo,
					"Room":       room,
				})
			} else {
				c.AbortWithStatus(http.StatusNotFound)
			}
		})

		roomRouter.GET("/:roomID/power_levels", func(c *gin.Context) {
			c.HTML(http.StatusOK, "power_levels.html", gin.H{
				"Room": c.MustGet("Room").(*mxclient.Room),
			})
		})
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	LoadPublicRooms(client, true)
	go startForwardPaginator(client)
	go startPublicRoomListTimer(client)
	fmt.Println("Listening on port " + port)

	srv := &http.Server{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		Handler:      router,
		Addr:         ":" + port,
	}

	panic(srv.ListenAndServe())
}

const LoadPublicRoomsPeriod = time.Hour

func startPublicRoomListTimer(client *mxclient.Client) {
	t := time.NewTicker(LoadPublicRoomsPeriod)
	for {
		<-t.C
		LoadPublicRooms(client, false)
	}
}

const LazyForwardPaginateRooms = time.Minute

func startForwardPaginator(client *mxclient.Client) {
	t := time.NewTicker(LazyForwardPaginateRooms)
	for {
		<-t.C
		for _, room := range client.GetRoomList(0, -1) {
			room.LazyUpdateRoom()
		}
	}
}
