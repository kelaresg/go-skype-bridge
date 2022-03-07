package main

func (puppet *Puppet) SetMatrixContacts(conatcts []string) (err error) {
	puppet.log.Debugf("SetMatrixContacts with %+v matrix ids", conatcts)

	if (len(conatcts) > 0) {
		client := puppet.CustomIntent().Client
		if (client != nil) {
			urlPath := client.BuildURL("account", "account_contacts")

			s := struct {
				ContactsType string `json:"contacts_type"`
				ContactsIds []string `json:"contacts_ids"`
				BridgeId   string `json:"bridge_id"`
			}{"skype", conatcts, string(puppet.MXID)}
			_, err = client.MakeRequest("POST", urlPath, &s, nil)
		}
	}

	return
}

