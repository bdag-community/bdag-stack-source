@echo off
if not exist "%~dp0.exe" goto :error

if not exist "%~dp0config.conf" goto :config
goto :version

:config
echo ______________________________________________________________
choice /C YN /M "BDAG 'config.conf' file not found, Do you wan to create an new one"

IF ERRORLEVEL ==2 GOTO :no
IF ERRORLEVEL ==1 GOTO :yes
GOTO :end
:no
ECHO Fail to start BDAG node because the 'config.conf' file not found.
GOTO :end
:yes
ECHO Create an new BDAG 'config.conf' file

echo datadir=./data >> config.conf
echo logdir=./data >> config.conf
echo rpclisten=0.0.0.0:8131 >> config.conf
echo rpcuser=Blockdag >> config.conf
echo rpcpass=Blockdag123 >> config.conf
echo notls=false >> config.conf
echo debuglevel=info >> config.conf

:version
echo ______________________________________________________________
echo Find BDAG node executable :
"%~dp0" --version


:warnweakpass
FINDSTR "rpcuser=Blockdag" config.conf
if %errorlevel% ==0 echo WARNING using default RPC user
FINDSTR "rpcpass=Blockdag123" config.conf
if %errorlevel% ==0 echo WARNING using default RPC password


choice /C YN /M "Do you wan to start the BDAG node"
IF ERRORLEVEL ==2 GOTO :end
IF ERRORLEVEL ==1 GOTO :run

:run
"%~dp0" -C=.\config.conf

echo ______________________________________________________________
echo.
goto :end
:error
echo [-] BDAG node executable not found.
echo Please extract all files from the downloaded package and check your package downloaded correctly from 'https://github.com/BlockdagNetworkLabs/bdag/releases'
:end
pause
