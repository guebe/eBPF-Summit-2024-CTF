
ba = bytearray.fromhex("47 47 71 49 72 77 58 55  77 75 57 6f 77 4c 75 45 57 48 70 67")

print(ba)

ba.reverse()

print("Backwards:")
print(ba)
print(' '.join(f'0x{x:02x}' for x in ba))
