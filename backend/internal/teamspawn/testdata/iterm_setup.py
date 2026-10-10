#!/usr/bin/python3
import json,os,sys,time
name=os.path.basename(sys.argv[0]);args=sys.argv[1:]
with open(os.environ['CC_HAHA_ITERM_SETUP_LOG'],'a') as f:f.write(json.dumps({'command':name,'args':args,'cwd':os.getcwd().replace(os.environ['HOME'],'HOME')})+'\n')
config=os.environ['CC_HAHA_ITERM_SETUP_CONFIG']
with open(config) as f:c=json.load(f)
if name=='which' and c.get('probeDelaySeconds',0)>0:time.sleep(c['probeDelaySeconds'])
if name=='which':
 found=c.get('available',{}).get(args[0],False)
 if found:print(os.path.join(os.environ['HOME'],'bin',args[0]))
 sys.exit(0 if found else 1)
if name in ['uv','pipx','pip','pip3']:
 error=c.get('installError',{}).get(name,'')
 if error:print(error,file=sys.stderr);sys.exit(1)
 c.setdefault('available',{})['it2']=True
 with open(config,'w') as f:json.dump(c,f)
 sys.exit(0)
if name=='it2':
 if not c.get('available',{}).get('it2',False):print('API not available',file=sys.stderr);sys.exit(1)
 error=c.get('verifyError','')
 if error:print(error,file=sys.stderr);sys.exit(1)
 print('private original sessions')
