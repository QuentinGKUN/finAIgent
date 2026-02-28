import subprocess, os, pathlib 
root=pathlib.Path(__file__).resolve().parent 
go_dir=root/'go-service' 
env=os.environ.copy() 
env['VALUECELL_API_URL']='http://127.0.0.1:8010/api/v1' 
env['VALUECELL_AGENT_NAME']='' 
log=open(root/'_go_backend.log','w',encoding='utf-8') 
subprocess.Popen([str(go_dir/'finassistant_go.exe')],cwd=str(go_dir),env=env,stdout=log,stderr=subprocess.STDOUT,creationflags=0x00000008 | 0x00000200) 
print('started') 
