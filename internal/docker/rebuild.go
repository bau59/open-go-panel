package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Existing runtime options come from Docker inspect, not from stale form data.
// The env file is short-lived and never printed in subprocess arguments.
type replacementSpec struct {
	ID string
	Name string
	Image string
	Running bool
	Labels map[string]string
	Env []string
	Ports map[string][]struct {
		HostIP string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}
	Mounts []struct {
		Type string `json:"Type"`
		Source string `json:"Source"`
		Name string `json:"Name"`
		Destination string `json:"Destination"`
		RW bool `json:"RW"`
		Driver string `json:"Driver"`
		Propagation string `json:"Propagation"`
	}
	Restart string
	MaxRetry int
	Init bool
	ShmSize int64
	NanoCPUs int64
	Memory int64
	MemorySwap int64
	NetworkMode string
	User string
	WorkDir string
	HasHealthcheck bool
}

func inspectReplacement(ctx context.Context, id string) (replacementSpec,error) {
	if !containerNamePattern.MatchString(id) {return replacementSpec{},errors.New("invalid container ID")}
	out,err:=exec.CommandContext(ctx,"docker","inspect",id).CombinedOutput()
	if err!=nil {return replacementSpec{},fmt.Errorf("inspect existing container: %w: %s",err,strings.TrimSpace(string(out)))}
	var items []struct{
		ID string `json:"Id"`
		Name string `json:"Name"`
		Config struct {
			Image string `json:"Image"`
			Env []string `json:"Env"`
			Labels map[string]string `json:"Labels"`
			User string `json:"User"`
			WorkingDir string `json:"WorkingDir"`
			Healthcheck json.RawMessage `json:"Healthcheck"`
		} `json:"Config"`
		State struct{Running bool `json:"Running"`} `json:"State"`
		HostConfig struct {
			PortBindings map[string][]struct {
				HostIP string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"PortBindings"`
			RestartPolicy struct {Name string `json:"Name"`; MaximumRetryCount int `json:"MaximumRetryCount"`} `json:"RestartPolicy"`
			Init *bool `json:"Init"`
			ShmSize int64 `json:"ShmSize"`
			NanoCPUs int64 `json:"NanoCpus"`
			Memory int64 `json:"Memory"`
			MemorySwap int64 `json:"MemorySwap"`
			NetworkMode string `json:"NetworkMode"`
			Privileged bool `json:"Privileged"`
			AutoRemove bool `json:"AutoRemove"`
			PublishAllPorts bool `json:"PublishAllPorts"`
			Devices []json.RawMessage `json:"Devices"`
			CapAdd []string `json:"CapAdd"`
			CapDrop []string `json:"CapDrop"`
			SecurityOpt []string `json:"SecurityOpt"`
			ExtraHosts []string `json:"ExtraHosts"`
			Dns []string `json:"Dns"`
			Binds []string `json:"Binds"`
		} `json:"HostConfig"`
		Mounts []struct {
			Type string `json:"Type"`
			Source string `json:"Source"`
			Name string `json:"Name"`
			Destination string `json:"Destination"`
			RW bool `json:"RW"`
			Driver string `json:"Driver"`
			Propagation string `json:"Propagation"`
		} `json:"Mounts"`
	}
	if err:=json.Unmarshal(out,&items);err!=nil{return replacementSpec{},fmt.Errorf("decode Docker inspect: %w",err)}
	if len(items)!=1 {return replacementSpec{},errors.New("Docker inspect must return one container")}
	i:=items[0]
	h:=i.HostConfig
	if h.Privileged||h.AutoRemove||h.PublishAllPorts||len(h.Devices)>0||len(h.CapAdd)>0||
		len(h.CapDrop)>0||len(h.SecurityOpt)>0||len(h.ExtraHosts)>0||len(h.Dns)>0 {
		return replacementSpec{},errors.New("container uses advanced Docker options; rebuild manually to avoid losing its settings")
	}
	if h.NetworkMode!="" && h.NetworkMode!="default" && h.NetworkMode!="bridge" {
		return replacementSpec{},fmt.Errorf("container uses network %q; automated rebuild is not supported",h.NetworkMode)
	}
	if !containerNamePattern.MatchString(strings.TrimPrefix(i.Name,"/")) {
		return replacementSpec{},errors.New("container name cannot be safely recreated")
	}
	p:=replacementSpec{ID:i.ID,Name:strings.TrimPrefix(i.Name,"/"),Image:i.Config.Image,
		Running:i.State.Running,Labels:i.Config.Labels,Env:i.Config.Env,Ports:h.PortBindings,
		Restart:h.RestartPolicy.Name,MaxRetry:h.RestartPolicy.MaximumRetryCount,
		ShmSize:h.ShmSize,NanoCPUs:h.NanoCPUs,Memory:h.Memory,MemorySwap:h.MemorySwap,
		NetworkMode:h.NetworkMode,User:i.Config.User,WorkDir:i.Config.WorkingDir,
		HasHealthcheck:len(i.Config.Healthcheck)>0 && string(i.Config.Healthcheck)!="null"}
	if h.Init!=nil {p.Init=*h.Init}
	for _,m:=range i.Mounts {
		p.Mounts=append(p.Mounts,struct {
			Type string `json:"Type"`
			Source string `json:"Source"`
			Name string `json:"Name"`
			Destination string `json:"Destination"`
			RW bool `json:"RW"`
			Driver string `json:"Driver"`
			Propagation string `json:"Propagation"`
		}{m.Type,m.Source,m.Name,m.Destination,m.RW,m.Driver,m.Propagation})
	}
	return p,nil
}

func (p replacementSpec) launchArgs(image string) ([]string,func(),error) {
	verb:="run"
	if !p.Running {verb="create"}
	args:=[]string{verb}
	if p.Running {args=append(args,"-d")}
	args=append(args,"--name",p.Name)
	if p.Restart!="" && p.Restart!="no" {
		restart:=p.Restart
		if restart=="on-failure" && p.MaxRetry>0 {restart+=":"+strconv.Itoa(p.MaxRetry)}
		switch p.Restart {case "always","unless-stopped","on-failure":default:return nil,nil,errors.New("unsupported restart policy")}
		args=append(args,"--restart",restart)
	}
	if p.Init {args=append(args,"--init")}
	if p.ShmSize>0 {args=append(args,"--shm-size",strconv.FormatInt(p.ShmSize,10))}
	if p.NanoCPUs>0 {args=append(args,"--cpus",strconv.FormatFloat(float64(p.NanoCPUs)/1e9,'f',-1,64))}
	if p.Memory>0 {args=append(args,"--memory",strconv.FormatInt(p.Memory,10))}
	if p.MemorySwap!=0 && p.Memory>0 {args=append(args,"--memory-swap",strconv.FormatInt(p.MemorySwap,10))}
	if p.User!="" {args=append(args,"--user",p.User)}
	if p.WorkDir!="" {args=append(args,"--workdir",p.WorkDir)}
	for port,bindings:=range p.Ports {
		if len(bindings)==0 {continue}
		for _,binding:=range bindings {
			protoParts:=strings.Split(port,"/")
			if len(protoParts)!=2 {return nil,nil,errors.New("invalid existing port mapping")}
			if _,err:=strconv.Atoi(protoParts[0]);err!=nil{return nil,nil,err}
			if binding.HostPort=="" {return nil,nil,errors.New("empty host port in Docker inspect")}
			host:=binding.HostIP
			if host!=""&&net.ParseIP(host)==nil{return nil,nil,errors.New("unsupported host IP")}
			if host=="" {host="0.0.0.0"}
			published:=host+":"+binding.HostPort+":"+port
			if strings.Contains(host,":"){published="["+host+"]:"+binding.HostPort+":"+port}
			args=append(args,"-p",published)
		}
	}
	for _,m:=range p.Mounts {
		if m.Destination==""||strings.ContainsAny(m.Destination,",\x00") {return nil,nil,errors.New("unsafe mount target")}
		var source string
		switch m.Type {
		case "bind":
			source=m.Source
			if !strings.HasPrefix(source,"/") {return nil,nil,errors.New("bind source is not absolute")}
		case "volume":
			if m.Driver!=""&&m.Driver!="local" {return nil,nil,errors.New("unsupported volume driver")}
			source=m.Name
		default:
			return nil,nil,fmt.Errorf("unsupported mount type %q",m.Type)
		}
		if source==""||strings.ContainsAny(source,",\x00") {return nil,nil,errors.New("unsafe mount source")}
		if m.Propagation!="" && m.Propagation!="rprivate" {
			return nil,nil,errors.New("custom mount propagation cannot be preserved")
		}
		spec:="type="+m.Type+",src="+source+",dst="+m.Destination
		if !m.RW {spec+=",readonly"}
		args=append(args,"--mount",spec)
	}
	cleanup:=func(){}
	if len(p.Env)>0 {
		file,err:=os.CreateTemp("","ogp-rebuild-env-")
		if err!=nil{return nil,nil,err}
		cleanup=func(){_ = os.Remove(file.Name())}
		for _,v:=range p.Env {
			if !strings.Contains(v,"=")||strings.ContainsAny(v,"\r\n\x00") {
				_ = file.Close();cleanup();return nil,nil,errors.New("existing environment contains an unsupported value")
			}
			if _,err:=file.WriteString(v+"\n");err!=nil{
				_ = file.Close();cleanup();return nil,nil,fmt.Errorf("write protected env file: %w",err)
			}
		}
		if err:=file.Close();err!=nil{cleanup();return nil,nil,err}
		args=append(args,"--env-file",file.Name())
	}
	args=append(args,image)
	return args,cleanup,nil
}

func (m *Manager) replaceWithBuiltImage(ctx context.Context, existingID, image string) error {
	// Re-inspect after the build to reject a target replaced by an external actor.
	// Serialize panel-driven lifecycle operations during the switch.
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	spec,err:=inspectReplacement(ctx,existingID)
	if err!=nil{return err}
	if spec.ID!=existingID{return errors.New("container changed while rebuilding; refusing replacement")}
	suffix:="-"+strconv.FormatInt(time.Now().UnixNano(),36)
	prefix:="ogp-prev-"
	name:=spec.Name
	if len(prefix)+len(name)+len(suffix)>127 {
		name=name[:127-len(prefix)-len(suffix)]
	}
	backupName:=prefix+name+suffix
	specArgs,cleanup,err:=spec.launchArgs(image)
	if err!=nil{return err}
	defer cleanup()
	if spec.Running {
		if err:=dockerCommand(ctx,"stop",existingID);err!=nil{return fmt.Errorf("stop old container: %w",err)}
	}
	if err:=dockerCommand(ctx,"rename",existingID,backupName);err!=nil {
		if spec.Running {_=dockerCommand(context.Background(),"start",existingID)}
		return fmt.Errorf("reserve old container for rollback: %w",err)
	}
	// The old container is retained but must not autostart in parallel on reboot.
	if err:=dockerCommand(ctx,"update","--restart=no",backupName);err!=nil{
		return rollbackReplacement(spec,backupName,image,err)
	}
	if err:=dockerCommand(ctx,specArgs...);err!=nil{
		return rollbackReplacement(spec,backupName,image,fmt.Errorf("start rebuilt container: %w",err))
	}
	if spec.Running {
		if err:=waitRebuiltContainer(ctx,spec.Name,spec.HasHealthcheck);err!=nil{
			return rollbackReplacement(spec,backupName,image,fmt.Errorf("new container did not become ready: %w",err))
		}
	}
	return nil
}

func rollbackReplacement(spec replacementSpec,backupName,image string,reason error) error {
	ctx,cancel:=context.WithTimeout(context.Background(),2*time.Minute)
	defer cancel()
	var problems []error
	// Remove a partially created replacement only if it actually uses the
	// image just built. Never delete a container created by another actor.
	if out,err:=exec.CommandContext(ctx,"docker","inspect","--format","{{.Config.Image}}",spec.Name).CombinedOutput();err==nil{
		if strings.TrimSpace(string(out))==image {
			if err:=dockerCommand(ctx,"rm","-f",spec.Name);err!=nil{
				problems=append(problems,fmt.Errorf("remove failed replacement: %w",err))
			}
		} else {
			problems=append(problems,errors.New("original container name is occupied by another image; refusing to remove it"))
		}
	}
	if err:=dockerCommand(ctx,"rename",backupName,spec.Name);err!=nil{
		problems=append(problems,fmt.Errorf("rename old container: %w",err))
	} else {
		restart:=spec.Restart
		if restart=="" {restart="no"}
		if restart=="on-failure"&&spec.MaxRetry>0 {restart+=":"+strconv.Itoa(spec.MaxRetry)}
		if err:=dockerCommand(ctx,"update","--restart="+restart,spec.Name);err!=nil{
			problems=append(problems,fmt.Errorf("restore autostart: %w",err))
		}
		if spec.Running {
			if err:=dockerCommand(ctx,"start",spec.Name);err!=nil {
				problems=append(problems,fmt.Errorf("restart previous container: %w",err))
			}
		}
	}
	if len(problems)>0 {
		return fmt.Errorf("%w; automatic rollback failed: %v; old container retained as %s",reason,errors.Join(problems...),backupName)
	}
	return fmt.Errorf("%w; previous container restored",reason)
}

func waitRebuiltContainer(ctx context.Context,name string,hasHealthcheck bool) error {
	limit:=3*time.Second
	if hasHealthcheck {limit=60*time.Second}
	timer:=time.NewTimer(limit)
	defer timer.Stop()
	ticker:=time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():return ctx.Err()
		case <-ticker.C:
			out,err:=exec.CommandContext(ctx,"docker","inspect","--format",
				"{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}",name).CombinedOutput()
			if err!=nil{return fmt.Errorf("inspect new container: %w",err)}
			fields:=strings.Split(strings.TrimSpace(string(out)),"|")
			if len(fields)!=2||fields[0]!="true" {return errors.New("container process exited")}
			if hasHealthcheck {
				if fields[1]=="healthy" {return nil}
				if fields[1]=="unhealthy" {return errors.New("Docker healthcheck failed")}
			} else if fields[1]=="none" {
				// The absence of a healthcheck can establish process survival only.
				// Wait for the full short observation period, not application readiness.
			}
		case <-timer.C:
			if hasHealthcheck {return errors.New("Docker healthcheck did not pass within 60 seconds")}
			out,err:=exec.CommandContext(ctx,"docker","inspect","--format","{{.State.Running}}",name).CombinedOutput()
			if err!=nil{return err}
			if strings.TrimSpace(string(out))!="true" {return errors.New("container process exited")}
			return nil
		}
	}
}
