<script setup lang="ts">
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger
      class="focus-visible:ring-ring/50 rounded-full outline-none focus-visible:ring-[3px]"
      aria-label="Operator menu"
    >
      <Avatar class="size-7">
        <AvatarFallback class="text-xs">A</AvatarFallback>
      </Avatar>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-48">
      <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">
        Signed in locally
      </DropdownMenuLabel>
      <DropdownMenuSeparator />
      <DropdownMenuItem as-child>
        <RouterLink to="/system/appearance" class="w-full cursor-pointer"
          >Appearance</RouterLink
        >
      </DropdownMenuItem>
      <DropdownMenuItem as-child>
        <RouterLink to="/system/status" class="w-full cursor-pointer"
          >Status</RouterLink
        >
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
