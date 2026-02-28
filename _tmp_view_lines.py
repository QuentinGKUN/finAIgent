import pathlib,sys 
p=pathlib.Path(sys.argv[1]) 
lines=p.read_text(encoding='utf-8').splitlines() 
s=int(sys.argv[2]); e=int(sys.argv[3]) 
for i in range(max(1,s), min(e,len(lines))+1): 
    print(f'{i}:{lines[i-1]}')
