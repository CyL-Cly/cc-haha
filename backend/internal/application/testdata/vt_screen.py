"""Small VT cell projection for public PTY assertions, including renderer diffs."""
import codecs,re,unicodedata

class VTScreen:
 def __init__(self,width,height):
  self.width,self.height=width,height;self.lines=[[' ']*width for _ in range(height)];self.row=0;self.col=0;self.pending='';self.saved=(0,0);self.decoder=codecs.getincrementaldecoder('utf8')('replace');self.scroll_top=0;self.scroll_bottom=height-1
 def advance(self):
  if self.row==self.scroll_bottom:
   top,bottom=self.scroll_top,self.scroll_bottom;self.lines[top:bottom+1]=self.lines[top+1:bottom+1]+[[' ']*self.width]
  elif self.row<self.height-1:self.row+=1
 def feed(self,data):
  text=self.pending+self.decoder.decode(data);self.pending='';i=0;width,height=self.width,self.height
  while i<len(text):
   c=text[i]
   if c=='\x1b':
    if i+1>=len(text):self.pending=text[i:];break
    if text[i+1]==']':
     match=re.search('\x07|\x1b\\\\',text[i+2:])
     if not match:self.pending=text[i:];break
     i+=2+match.end();continue
    if text[i+1]=='[':
     match=re.match(r'\x1b\[([0-?]*)([ -/]*)([@-~])',text[i:])
     if not match:self.pending=text[i:];break
     raw,_,op=match.groups();values=[int(v) if v else 0 for v in raw.split(';')] if re.fullmatch(r'[0-9;]*',raw) else [];n=(values[0] or 1) if values else 1
     if op=='A':self.row=max(0,self.row-n)
     elif op=='B':self.row=min(height-1,self.row+n)
     elif op=='C':self.col=min(width-1,self.col+n)
     elif op=='D':self.col=max(0,self.col-n)
     elif op in ('H','f'):self.row=min(height-1,max(0,n-1));self.col=min(width-1,max(0,(values[1] or 1)-1 if len(values)>1 else 0))
     elif op=='G':self.col=min(width-1,max(0,n-1))
     elif op=='d':self.row=min(height-1,max(0,n-1))
     elif op=='E':self.row=min(height-1,self.row+n);self.col=0
     elif op=='F':self.row=max(0,self.row-n);self.col=0
     elif op=='K':
      mode=values[0] if values else 0;start=0 if mode in (1,2) else self.col;end=self.col+1 if mode==1 else width;self.lines[self.row][start:end]=[' ']*(end-start)
     elif op=='J':
      mode=values[0] if values else 0
      if mode in (2,3):self.lines=[[' ']*width for _ in range(height)]
      elif mode==0:self.lines[self.row][self.col:]=[' ']*(width-self.col);self.lines[self.row+1:]=[[' ']*width for _ in range(height-self.row-1)]
     elif op=='X':self.lines[self.row][self.col:min(width,self.col+n)]=[' ']*min(n,width-self.col)
     elif op=='P':self.lines[self.row][self.col:]=self.lines[self.row][min(width,self.col+n):]+[' ']*min(n,width-self.col)
     elif op=='@':self.lines[self.row][self.col:]=([' ']*n+self.lines[self.row][self.col:])[:width-self.col]
     elif op in ('S','T'):
      top,bottom=self.scroll_top,self.scroll_bottom;count=min(n,bottom-top+1);region=self.lines[top:bottom+1]
      self.lines[top:bottom+1]=region[count:]+[[' ']*width for _ in range(count)] if op=='S' else [[' ']*width for _ in range(count)]+region[:len(region)-count]
     elif op in ('L','M') and self.scroll_top<=self.row<=self.scroll_bottom:
      top,bottom=self.row,self.scroll_bottom;count=min(n,bottom-top+1);region=self.lines[top:bottom+1]
      self.lines[top:bottom+1]=[[' ']*width for _ in range(count)]+region[:len(region)-count] if op=='L' else region[count:]+[[' ']*width for _ in range(count)]
     elif op=='r':
      top=n-1;bottom=(values[1] or height)-1 if len(values)>1 else height-1
      if 0<=top<bottom<height:self.scroll_top,self.scroll_bottom=top,bottom;self.row=0;self.col=0
     elif op=='s':self.saved=(self.row,self.col)
     elif op=='u':self.row,self.col=self.saved
     i+=match.end();continue
    if text[i+1]=='7':self.saved=(self.row,self.col)
    elif text[i+1]=='8':self.row,self.col=self.saved
    elif text[i+1]=='M':
     if self.row==self.scroll_top:
      top,bottom=self.scroll_top,self.scroll_bottom;self.lines[top:bottom+1]=[[' ']*width]+self.lines[top:bottom]
     else:self.row-=1
    i+=2;continue
   if c=='\r':self.col=0
   elif c=='\n':self.advance()
   elif c=='\b':self.col=max(0,self.col-1)
   elif c=='\t':self.col=min(width-1,(self.col//8+1)*8)
   elif ord(c)>=32:
    if self.col>=width:self.col=0;self.advance()
    cells=2 if unicodedata.east_asian_width(c) in ('W','F') else 1
    if not unicodedata.combining(c):self.lines[self.row][self.col]=c;self.col+=cells
   i+=1
 def text(self):return '\n'.join(''.join(line).rstrip() for line in self.lines)
