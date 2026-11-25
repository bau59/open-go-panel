package dbmanager

import "testing"

func TestCloudBackupName(t *testing.T){
    for _,name:=range []string{"20261125T010203.000000000Z.sql.gz","backup.sql.gz"}{
        if !cloudBackupName.MatchString(name){t.Errorf("rejected %s",name)}
    }
    for _,name:=range []string{"../backup.sql.gz","a/b.sql.gz","/backup.sql.gz",".hidden.sql.gz","backup.sql","backup.sql.gz/","--flag.sql.gz"}{
        if cloudBackupName.MatchString(name){t.Errorf("accepted %s",name)}
    }
    if cloudBackupDate("20261125T010203.000000000Z.sql.gz").IsZero(){t.Fatal("timestamp not parsed")}
}
