package model

import "time"

const (
	TypeTask    = 0
	TypeProject = 1

	StatusOpen      = 0
	StatusCancelled = 2
	StatusCompleted = 3

	StartInbox   = 0
	StartAnytime = 1
	StartSomeday = 2
)

// ThingsDate is a bit-encoded date: year<<16 | month<<12 | day<<7.
type ThingsDate int64

func (d ThingsDate) ToTime() time.Time {
	year := int(d >> 16)
	month := time.Month((int(d) >> 12) & 0xF)
	day := (int(d) >> 7) & 0x1F
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

func ThingsDateFromTime(t time.Time) ThingsDate {
	return ThingsDate(t.Year()<<16 | int(t.Month())<<12 | t.Day()<<7)
}

func (d ThingsDate) String() string {
	return d.ToTime().Format("2006-01-02")
}

var coreDataEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// CoreDataToTime converts a Core Data timestamp (seconds since 2001-01-01) to time.Time.
func CoreDataToTime(ts float64) time.Time {
	return coreDataEpoch.Add(time.Duration(ts * float64(time.Second)))
}

// TimeToCoreData converts a time.Time to Core Data timestamp.
func TimeToCoreData(t time.Time) float64 {
	return t.Sub(coreDataEpoch).Seconds()
}

type Task struct {
	UUID         string      `json:"uuid"`
	Title        string      `json:"title"`
	Notes        string      `json:"notes,omitempty"`
	Type         int         `json:"type"`
	Status       int         `json:"status"`
	Start        int         `json:"start"`
	StartBucket  int         `json:"startBucket"`
	StartDate    *ThingsDate `json:"startDate,omitempty"`
	Deadline     *ThingsDate `json:"deadline,omitempty"`
	StopDate     *time.Time  `json:"stopDate,omitempty"`
	CreationDate *time.Time  `json:"creationDate,omitempty"`
	Trashed      bool        `json:"trashed"`
	ProjectUUID  string      `json:"projectUUID,omitempty"`
	ProjectTitle string      `json:"projectTitle,omitempty"`
	AreaUUID     string      `json:"areaUUID,omitempty"`
	AreaTitle    string      `json:"areaTitle,omitempty"`
	HeadingUUID  string      `json:"headingUUID,omitempty"`
	HeadingTitle string      `json:"headingTitle,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Index        int         `json:"index"`
	TodayIndex   int         `json:"todayIndex"`
}

type ChecklistItem struct {
	UUID     string     `json:"uuid"`
	Title    string     `json:"title"`
	Status   int        `json:"status"`
	StopDate *time.Time `json:"stopDate,omitempty"`
	Index    int        `json:"index"`
}

type Project struct {
	UUID      string   `json:"uuid"`
	Title     string   `json:"title"`
	Status    int      `json:"status"`
	AreaUUID  string   `json:"areaUUID,omitempty"`
	AreaTitle string   `json:"areaTitle,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	TaskCount int      `json:"taskCount"`
	OpenCount int      `json:"openCount"`
}

type Area struct {
	UUID    string `json:"uuid"`
	Title   string `json:"title"`
	Visible bool   `json:"visible"`
}

type Tag struct {
	UUID       string `json:"uuid"`
	Title      string `json:"title"`
	Shortcut   string `json:"shortcut,omitempty"`
	ParentUUID string `json:"parentUUID,omitempty"`
}
