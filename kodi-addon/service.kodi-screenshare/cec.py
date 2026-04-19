"""HDMI-CEC helper script callable via Addons.ExecuteAddon."""

import sys
import urllib.parse

import xbmc


def main() -> None:
    params = urllib.parse.parse_qs("&".join(sys.argv[1:]))
    command = params.get("command", [""])[0]

    if command == "activate":
        xbmc.executebuiltin("CECActivateSource()")
    elif command == "standby":
        xbmc.executebuiltin("CECStandby()")
    elif command == "toggle":
        xbmc.executebuiltin("CECToggleState()")


if __name__ == "__main__":
    main()
