package skypeExt

import skype "github.com/kelaresg/go-skypeapi"

type Presence struct {
	Id string
	Availability string
	Status skype.Presence
}